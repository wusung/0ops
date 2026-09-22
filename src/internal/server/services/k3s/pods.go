package k3s

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

var podGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

// ManagedPodSelector matches pods this platform rendered. Everything the
// allocation ledger knows about a pod's owner comes from labels, never
// from parsing its name (resource-usage-metering spec § 4.3).
const ManagedPodSelector = "app.0ops.io/managed-by=0ops"

const (
	labelAppSlug  = "app.0ops.io/slug"
	labelTeamSlug = "app.0ops.io/team"
	gpuResource   = "nvidia.com/gpu"
)

// PodSnapshot is the subset of a pod the allocation ledger needs, with
// every timestamp taken from the object itself rather than from the
// reader's clock (spec § 14 rule #1). Keeping this a plain struct lets
// the ledger logic be a pure function with no cluster in sight.
type PodSnapshot struct {
	UID       string
	Name      string
	Namespace string
	AppSlug   string
	TeamSlug  string

	// StartedAt is status.startTime: the moment kubelet accepted the pod.
	// Nil while the pod is unscheduled — such a pod holds no node
	// resources yet and must not open an interval.
	StartedAt *time.Time

	// TerminatedAt is the latest finishedAt across containers, set only
	// once every container has terminated. Authoritative.
	TerminatedAt *time.Time

	// DeletedAt is metadata.deletionTimestamp. Authoritative, but weaker
	// than TerminatedAt: it marks when deletion was requested.
	DeletedAt *time.Time

	// Ready reflects the Ready condition. Irrelevant to allocation — a
	// pod holds its resources whether or not it passes its probes — but
	// it is what the observed-usage track records as "active".
	Ready bool

	// Phase is status.phase. A pod only stops holding its allocation
	// once it reaches a terminal phase; a container that exited and is
	// waiting to restart has not.
	Phase string

	// Allocation, summed across containers. Requests for cpu/memory
	// (LimitRange defaults are already materialised into the pod spec by
	// admission, so this covers both user-specified and defaulted pods),
	// limits for GPU, which has no request semantics.
	CPUMillicores int
	MemoryBytes   int64
	GPUCount      int
	GPUType       string
}

// ListManagedPods returns every platform-managed pod across all
// namespaces. Cluster-scoped by necessity: team namespaces are created
// on demand, so there is no fixed list to iterate.
func (c *Client) ListManagedPods(ctx context.Context) ([]PodSnapshot, error) {
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("k3s dynamic client not initialized")
	}
	list, err := c.dynamicClient.Resource(podGVR).Namespace(metav1.NamespaceAll).
		List(ctx, metav1.ListOptions{LabelSelector: ManagedPodSelector})
	if err != nil {
		return nil, fmt.Errorf("list managed pods: %w", err)
	}
	out := make([]PodSnapshot, 0, len(list.Items))
	for i := range list.Items {
		snap, err := PodSnapshotFrom(&list.Items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, nil
}

// PodSnapshotFrom projects an unstructured pod onto PodSnapshot. Exported
// so the watch path can share exactly one parsing implementation with the
// relist path — two parsers would eventually disagree about a timestamp.
func PodSnapshotFrom(obj *unstructured.Unstructured) (PodSnapshot, error) {
	snap := PodSnapshot{
		UID:       string(obj.GetUID()),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}
	labels := obj.GetLabels()
	snap.AppSlug = labels[labelAppSlug]
	snap.TeamSlug = labels[labelTeamSlug]

	if ts := obj.GetDeletionTimestamp(); ts != nil {
		t := ts.Time.UTC()
		snap.DeletedAt = &t
	}

	if raw, found, _ := unstructured.NestedString(obj.Object, "status", "startTime"); found && raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return PodSnapshot{}, fmt.Errorf("pod %s/%s: parse startTime %q: %w", snap.Namespace, snap.Name, raw, err)
		}
		t = t.UTC()
		snap.StartedAt = &t
	}

	containers, _, err := unstructured.NestedSlice(obj.Object, "spec", "containers")
	if err != nil {
		return PodSnapshot{}, fmt.Errorf("pod %s/%s: read containers: %w", snap.Namespace, snap.Name, err)
	}
	// Native sidecars — initContainers with restartPolicy: Always — run
	// for the pod's whole life and the scheduler reserves their requests
	// the whole time, so they are part of the allocation. Plain init
	// containers are not: they finish before the app starts, and K8s
	// takes the max rather than the sum, which the regular containers
	// almost always dominate. Ignoring those errs downward.
	initContainers, _, err := unstructured.NestedSlice(obj.Object, "spec", "initContainers")
	if err != nil {
		return PodSnapshot{}, fmt.Errorf("pod %s/%s: read initContainers: %w", snap.Namespace, snap.Name, err)
	}
	for _, raw := range initContainers {
		c, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if policy, _, _ := unstructured.NestedString(c, "restartPolicy"); policy != "Always" {
			continue
		}
		containers = append(containers, raw)
	}
	for _, raw := range containers {
		c, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		cpu, mem, gpu, err := containerAllocation(c)
		if err != nil {
			return PodSnapshot{}, fmt.Errorf("pod %s/%s: %w", snap.Namespace, snap.Name, err)
		}
		snap.CPUMillicores += cpu
		snap.MemoryBytes += mem
		snap.GPUCount += gpu
	}
	if snap.GPUCount > 0 {
		snap.GPUType, _, _ = unstructured.NestedString(obj.Object, "spec", "nodeSelector", "0ops.io/gpu-type")
	}

	snap.Phase, _, _ = unstructured.NestedString(obj.Object, "status", "phase")
	snap.Ready = podIsReady(obj)

	terminated, err := terminationTime(obj)
	if err != nil {
		return PodSnapshot{}, fmt.Errorf("pod %s/%s: %w", snap.Namespace, snap.Name, err)
	}
	snap.TerminatedAt = terminated

	return snap, nil
}

// podIsReady reports the Ready condition. A pod with no conditions yet
// is not ready.
func podIsReady(obj *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conditions {
		c, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _, _ := unstructured.NestedString(c, "type"); t != "Ready" {
			continue
		}
		status, _, _ := unstructured.NestedString(c, "status")
		return status == "True"
	}
	return false
}

func containerAllocation(c map[string]interface{}) (cpuMillis int, memBytes int64, gpu int, err error) {
	if raw, found, _ := unstructured.NestedString(c, "resources", "requests", "cpu"); found && raw != "" {
		q, perr := resource.ParseQuantity(raw)
		if perr != nil {
			return 0, 0, 0, fmt.Errorf("parse cpu request %q: %w", raw, perr)
		}
		cpuMillis = int(q.MilliValue())
	}
	if raw, found, _ := unstructured.NestedString(c, "resources", "requests", "memory"); found && raw != "" {
		q, perr := resource.ParseQuantity(raw)
		if perr != nil {
			return 0, 0, 0, fmt.Errorf("parse memory request %q: %w", raw, perr)
		}
		memBytes = q.Value()
	}
	// GPUs are allocated through limits; there is no request form.
	if raw, found, _ := unstructured.NestedString(c, "resources", "limits", gpuResource); found && raw != "" {
		q, perr := resource.ParseQuantity(raw)
		if perr != nil {
			return 0, 0, 0, fmt.Errorf("parse gpu limit %q: %w", raw, perr)
		}
		gpu = int(q.Value())
	}
	return cpuMillis, memBytes, gpu, nil
}

// terminationTime returns the latest container finishedAt, but only for
// a pod that has actually reached a terminal phase.
//
// Checking container states alone is not enough: a crash-looping pod
// with restartPolicy: Always briefly carries a real
// state.terminated.finishedAt between the exit and kubelet rewriting it
// to waiting/CrashLoopBackOff. Reading that as "the pod ended" closes
// the interval on a pod that is still scheduled and still holding its
// node capacity — and since pod_uid is the primary key, it could never
// be reopened. The phase gate is what makes a restart a non-event.
func terminationTime(obj *unstructured.Unstructured) (*time.Time, error) {
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	if phase != "Succeeded" && phase != "Failed" {
		return nil, nil
	}
	statuses, found, err := unstructured.NestedSlice(obj.Object, "status", "containerStatuses")
	if err != nil {
		return nil, fmt.Errorf("read containerStatuses: %w", err)
	}
	if !found || len(statuses) == 0 {
		return nil, nil
	}
	var latest time.Time
	for _, raw := range statuses {
		st, ok := raw.(map[string]interface{})
		if !ok {
			return nil, nil
		}
		finished, found, _ := unstructured.NestedString(st, "state", "terminated", "finishedAt")
		if !found || finished == "" {
			return nil, nil
		}
		t, perr := time.Parse(time.RFC3339, finished)
		if perr != nil {
			return nil, fmt.Errorf("parse finishedAt %q: %w", finished, perr)
		}
		if t.After(latest) {
			latest = t.UTC()
		}
	}
	if latest.IsZero() {
		return nil, nil
	}
	return &latest, nil
}

// PodEventHandler receives pod lifecycle transitions. DELETE carries the
// object's final state when the API server provided one; when it did not
// (a missed watch window), tombstone is true and the snapshot is whatever
// the cache last held.
type PodEventHandler interface {
	OnPodUpsert(ctx context.Context, pod PodSnapshot)
	OnPodDelete(ctx context.Context, pod PodSnapshot, tombstone bool)
}

// WatchManagedPods streams pod lifecycle events until ctx is cancelled.
//
// Watching does not make the ledger more accurate in principle — interval
// boundaries always come from the pod object, not from when the event
// arrived. What it buys is timeliness: a pod deleted by a rolling update
// can disappear long before the next relist, and a DELETE event carries
// the final object with its authoritative finishedAt, where a relist
// would only be able to infer the end time conservatively.
func (c *Client) WatchManagedPods(ctx context.Context, handler PodEventHandler) error {
	if c.dynamicClient == nil {
		return fmt.Errorf("k3s dynamic client not initialized")
	}
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		c.dynamicClient, 0, metav1.NamespaceAll,
		func(opts *metav1.ListOptions) { opts.LabelSelector = ManagedPodSelector },
	)
	informer := factory.ForResource(podGVR).Informer()

	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if snap, ok := snapshotFromEvent(obj); ok {
				handler.OnPodUpsert(ctx, snap)
			}
		},
		UpdateFunc: func(_, obj interface{}) {
			if snap, ok := snapshotFromEvent(obj); ok {
				handler.OnPodUpsert(ctx, snap)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// A tombstone means the final object was lost. Rather than
			// guess an end time, hand it over flagged so the ledger can
			// fall back to its conservative path.
			if stale, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				if snap, ok := snapshotFromEvent(stale.Obj); ok {
					handler.OnPodDelete(ctx, snap, true)
				}
				return
			}
			if snap, ok := snapshotFromEvent(obj); ok {
				handler.OnPodDelete(ctx, snap, false)
			}
		},
	}); err != nil {
		return fmt.Errorf("register pod event handler: %w", err)
	}

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("pod informer cache did not sync")
	}
	<-ctx.Done()
	return ctx.Err()
}

func snapshotFromEvent(obj interface{}) (PodSnapshot, bool) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return PodSnapshot{}, false
	}
	snap, err := PodSnapshotFrom(u)
	if err != nil {
		// A pod we cannot parse is one we must not bill. The relist pass
		// will surface it again; silently guessing at zero would
		// under-bill invisibly.
		return PodSnapshot{}, false
	}
	return snap, true
}

// PodUsage is one pod's instantaneous resource consumption as reported
// by metrics-server.
//
// This is a gauge: it says what the pod was using at the moment of the
// read and nothing about the interval between reads. It is therefore
// unfit for metering (ADR-0018) and is only used to answer "is this app
// sized sensibly".
type PodUsage struct {
	UID           string
	Name          string
	Namespace     string
	CPUMillicores int
	MemoryBytes   int64
}

var podMetricsGVR = schema.GroupVersionResource{
	Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods",
}

// ErrMetricsAPIUnavailable reports that metrics-server is absent or not
// answering. Callers must skip the tick rather than substitute declared
// resources, which would silently turn allocation into fake usage.
var ErrMetricsAPIUnavailable = errors.New("metrics API unavailable")

// ListPodUsage reads instantaneous usage for managed pods across all
// namespaces. Pods with no metrics yet (just started, or metrics-server
// has not scraped them) are simply absent from the result.
func (c *Client) ListPodUsage(ctx context.Context) ([]PodUsage, error) {
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("k3s dynamic client not initialized")
	}
	list, err := c.dynamicClient.Resource(podMetricsGVR).Namespace(metav1.NamespaceAll).
		List(ctx, metav1.ListOptions{LabelSelector: ManagedPodSelector})
	if err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsServiceUnavailable(err) || meta.IsNoMatchError(err) {
			return nil, fmt.Errorf("%w: %v", ErrMetricsAPIUnavailable, err)
		}
		return nil, fmt.Errorf("list pod metrics: %w", err)
	}

	out := make([]PodUsage, 0, len(list.Items))
	for i := range list.Items {
		usage, err := podUsageFrom(&list.Items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, usage)
	}
	return out, nil
}

func podUsageFrom(obj *unstructured.Unstructured) (PodUsage, error) {
	u := PodUsage{
		UID:       string(obj.GetUID()),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}
	containers, _, err := unstructured.NestedSlice(obj.Object, "containers")
	if err != nil {
		return PodUsage{}, fmt.Errorf("pod metrics %s/%s: read containers: %w", u.Namespace, u.Name, err)
	}
	for _, raw := range containers {
		c, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		// metrics-server reports cpu in nanocores ("1234n") and memory
		// in Ki; Quantity handles both without special-casing.
		if v, found, _ := unstructured.NestedString(c, "usage", "cpu"); found && v != "" {
			q, perr := resource.ParseQuantity(v)
			if perr != nil {
				return PodUsage{}, fmt.Errorf("pod metrics %s/%s: parse cpu %q: %w", u.Namespace, u.Name, v, perr)
			}
			u.CPUMillicores += int(q.MilliValue())
		}
		if v, found, _ := unstructured.NestedString(c, "usage", "memory"); found && v != "" {
			q, perr := resource.ParseQuantity(v)
			if perr != nil {
				return PodUsage{}, fmt.Errorf("pod metrics %s/%s: parse memory %q: %w", u.Namespace, u.Name, v, perr)
			}
			u.MemoryBytes += q.Value()
		}
	}
	return u, nil
}
