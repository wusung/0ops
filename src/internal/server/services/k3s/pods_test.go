package k3s

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func podObject(mods ...func(map[string]interface{})) *unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"uid":       "11111111-1111-1111-1111-111111111111",
			"name":      "web-abc",
			"namespace": "team-acme",
			"labels": map[string]interface{}{
				"app.0ops.io/slug":       "web",
				"app.0ops.io/team":       "acme",
				"app.0ops.io/managed-by": "0ops",
			},
		},
		"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{
					"name": "app",
					"resources": map[string]interface{}{
						"requests": map[string]interface{}{
							"cpu":    "100m",
							"memory": "256Mi",
						},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"startTime": "2026-09-22T12:00:00Z",
			"phase":     "Running",
		},
	}
	for _, m := range mods {
		m(obj)
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestPodSnapshotReadsLabelsAndAllocation(t *testing.T) {
	snap, err := PodSnapshotFrom(podObject())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.AppSlug != "web" || snap.TeamSlug != "acme" {
		t.Errorf("ownership labels: %+v", snap)
	}
	if snap.CPUMillicores != 100 {
		t.Errorf("CPUMillicores = %d, want 100", snap.CPUMillicores)
	}
	if snap.MemoryBytes != 256*1024*1024 {
		t.Errorf("MemoryBytes = %d, want %d", snap.MemoryBytes, 256*1024*1024)
	}
	if snap.StartedAt == nil || !snap.StartedAt.Equal(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("StartedAt = %v", snap.StartedAt)
	}
}

// Quantities arrive in whatever form the writer used; all of them have to
// land on the same canonical number.
func TestQuantityFormsNormalise(t *testing.T) {
	cases := []struct {
		cpu     string
		memory  string
		wantCPU int
		wantMem int64
	}{
		{"100m", "256Mi", 100, 268435456},
		{"2", "1Gi", 2000, 1073741824},
		{"1500m", "524288Ki", 1500, 536870912},
		{"0.25", "1000000", 250, 1000000},
	}
	for _, c := range cases {
		obj := podObject(func(o map[string]interface{}) {
			containers := o["spec"].(map[string]interface{})["containers"].([]interface{})
			req := containers[0].(map[string]interface{})["resources"].(map[string]interface{})["requests"].(map[string]interface{})
			req["cpu"] = c.cpu
			req["memory"] = c.memory
		})
		snap, err := PodSnapshotFrom(obj)
		if err != nil {
			t.Fatalf("parse %s/%s: %v", c.cpu, c.memory, err)
		}
		if snap.CPUMillicores != c.wantCPU || snap.MemoryBytes != c.wantMem {
			t.Errorf("%s/%s → %d millicores, %d bytes; want %d, %d",
				c.cpu, c.memory, snap.CPUMillicores, snap.MemoryBytes, c.wantCPU, c.wantMem)
		}
	}
}

func TestMultiContainerAllocationSums(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		spec := o["spec"].(map[string]interface{})
		spec["containers"] = append(spec["containers"].([]interface{}), map[string]interface{}{
			"name": "sidecar",
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{"cpu": "50m", "memory": "64Mi"},
			},
		})
	})
	snap, err := PodSnapshotFrom(obj)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.CPUMillicores != 150 || snap.MemoryBytes != (256+64)*1024*1024 {
		t.Errorf("sidecar allocation not counted: %+v", snap)
	}
}

func TestGPUReadFromLimits(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		containers := o["spec"].(map[string]interface{})["containers"].([]interface{})
		res := containers[0].(map[string]interface{})["resources"].(map[string]interface{})
		res["limits"] = map[string]interface{}{"nvidia.com/gpu": "2"}
		o["spec"].(map[string]interface{})["nodeSelector"] = map[string]interface{}{"0ops.io/gpu-type": "nvidia-t4"}
	})
	snap, err := PodSnapshotFrom(obj)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.GPUCount != 2 || snap.GPUType != "nvidia-t4" {
		t.Errorf("GPU allocation: count=%d type=%q", snap.GPUCount, snap.GPUType)
	}
}

// A pod with one container still running holds its whole allocation, so
// there is no end time yet.
// A crash-looping pod briefly carries a real finishedAt while kubelet
// is between the exit and the restart. Reading that as the end of the
// pod closes the interval on something still holding node capacity —
// and pod_uid being the primary key, it could never be reopened.
func TestCrashLoopingPodIsNotTreatedAsTerminated(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		spec := o["spec"].(map[string]interface{})
		spec["restartPolicy"] = "Always"
		status := o["status"].(map[string]interface{})
		status["phase"] = "Running"
		status["containerStatuses"] = []interface{}{
			map[string]interface{}{
				"restartCount": int64(3),
				"state": map[string]interface{}{
					"terminated": map[string]interface{}{"finishedAt": "2026-09-22T12:05:00Z"}},
			},
		}
	})
	snap, err := PodSnapshotFrom(obj)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.TerminatedAt != nil {
		t.Errorf("TerminatedAt = %v for a pod that is restarting, not ending", snap.TerminatedAt)
	}
}

func TestTerminalPhaseIsRequiredForTermination(t *testing.T) {
	for _, tc := range []struct {
		phase    string
		wantTerm bool
	}{
		{"Succeeded", true},
		{"Failed", true},
		{"Running", false},
		{"Pending", false},
		{"", false},
	} {
		obj := podObject(func(o map[string]interface{}) {
			status := o["status"].(map[string]interface{})
			status["phase"] = tc.phase
			status["containerStatuses"] = []interface{}{
				map[string]interface{}{"state": map[string]interface{}{
					"terminated": map[string]interface{}{"finishedAt": "2026-09-22T12:05:00Z"}}},
			}
		})
		snap, err := PodSnapshotFrom(obj)
		if err != nil {
			t.Fatalf("phase %q: %v", tc.phase, err)
		}
		if got := snap.TerminatedAt != nil; got != tc.wantTerm {
			t.Errorf("phase %q → terminated=%v, want %v", tc.phase, got, tc.wantTerm)
		}
	}
}

// A native sidecar holds its requests for the pod's whole life, so the
// scheduler reserves them the whole time. Missing them under-reports by
// however large the sidecar is — an order of magnitude in the worst case.
func TestNativeSidecarCountsTowardAllocation(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		spec := o["spec"].(map[string]interface{})
		spec["initContainers"] = []interface{}{
			map[string]interface{}{
				"name":          "proxy",
				"restartPolicy": "Always",
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"cpu": "2", "memory": "4Gi"}},
			},
			map[string]interface{}{
				// Plain init container: finishes before the app starts.
				"name": "migrate",
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"cpu": "8", "memory": "16Gi"}},
			},
		}
	})
	snap, err := PodSnapshotFrom(obj)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := 100 + 2000; snap.CPUMillicores != want {
		t.Errorf("CPUMillicores = %d, want %d (app + sidecar, not the plain init container)",
			snap.CPUMillicores, want)
	}
	if want := int64(256<<20 + 4<<30); snap.MemoryBytes != want {
		t.Errorf("MemoryBytes = %d, want %d", snap.MemoryBytes, want)
	}
}

func TestTerminationRequiresEveryContainerStopped(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		o["status"].(map[string]interface{})["phase"] = "Failed"
		o["status"].(map[string]interface{})["containerStatuses"] = []interface{}{
			map[string]interface{}{"state": map[string]interface{}{
				"terminated": map[string]interface{}{"finishedAt": "2026-09-22T12:05:00Z"}}},
			map[string]interface{}{"state": map[string]interface{}{
				"running": map[string]interface{}{"startedAt": "2026-09-22T12:00:00Z"}}},
		}
	})
	snap, err := PodSnapshotFrom(obj)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.TerminatedAt != nil {
		t.Errorf("TerminatedAt = %v while a container is still running", snap.TerminatedAt)
	}
}

func TestTerminationTakesLatestContainerFinish(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		o["status"].(map[string]interface{})["phase"] = "Succeeded"
		o["status"].(map[string]interface{})["containerStatuses"] = []interface{}{
			map[string]interface{}{"state": map[string]interface{}{
				"terminated": map[string]interface{}{"finishedAt": "2026-09-22T12:05:00Z"}}},
			map[string]interface{}{"state": map[string]interface{}{
				"terminated": map[string]interface{}{"finishedAt": "2026-09-22T12:07:30Z"}}},
		}
	})
	snap, err := PodSnapshotFrom(obj)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := time.Date(2026, 9, 22, 12, 7, 30, 0, time.UTC)
	if snap.TerminatedAt == nil || !snap.TerminatedAt.Equal(want) {
		t.Errorf("TerminatedAt = %v, want %v", snap.TerminatedAt, want)
	}
}

func TestUnscheduledPodHasNoStartTime(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		delete(o["status"].(map[string]interface{}), "startTime")
	})
	snap, err := PodSnapshotFrom(obj)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if snap.StartedAt != nil {
		t.Errorf("StartedAt = %v for an unscheduled pod", snap.StartedAt)
	}
}

// A malformed quantity must surface rather than silently become zero —
// zero is a valid allocation and would under-bill invisibly.
func TestMalformedQuantityIsAnError(t *testing.T) {
	obj := podObject(func(o map[string]interface{}) {
		containers := o["spec"].(map[string]interface{})["containers"].([]interface{})
		req := containers[0].(map[string]interface{})["resources"].(map[string]interface{})["requests"].(map[string]interface{})
		req["cpu"] = "not-a-quantity"
	})
	if _, err := PodSnapshotFrom(obj); err == nil {
		t.Fatal("want an error for an unparseable cpu request")
	}
}
