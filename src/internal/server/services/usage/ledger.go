// Package usage maintains the allocation ledger: one interval per pod,
// recording what the pod was allocated and for how long.
//
// The billing basis is (declared allocation) x (lifetime in seconds).
// Both factors are known constants, so integrating them is a closed-form
// calculation accurate to the second — unlike sampling a gauge, where
// accuracy is capped by how often you look and short-lived pods vanish
// entirely. See docs/features/resource-usage-metering/spec.md.
package usage

import (
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/server/services/k3s"
)

// AppRef identifies the app a pod belongs to, resolved from pod labels.
type AppRef struct {
	TeamID string
	AppID  string
}

// Close is a pending close operation on an open interval.
type Close struct {
	PodUID    string
	EndedAt   time.Time
	Estimated bool
	Reason    string
}

// Plan is what a single reconcile pass decided to do. It is data, not
// action: Reconcile computes it from two snapshots and nothing else, so
// every rule below is testable without a cluster or a database.
type Plan struct {
	Opens   []db.AllocationInterval
	Closes  []Close
	Touches []string

	// OrphanPods counts pods whose labels point at an app that no longer
	// exists. They are skipped, never guessed at.
	OrphanPods int
	// UnscheduledPods counts pods with no startTime: not yet bound to a
	// node, therefore holding no resources and owing nothing.
	UnscheduledPods int
}

// Reconcile diffs the cluster against the ledger.
//
// pods is every managed pod currently on the cluster; open is every
// interval the ledger still considers live; apps maps "<namespace>/<app
// slug>" to its owning ids. now is used only as the last-resort
// last_seen_at stamp for pods that are present — never as an interval
// boundary, which always comes from the pod object itself.
func Reconcile(pods []k3s.PodSnapshot, open []db.OpenIntervalRef, apps map[string]AppRef, now time.Time) Plan {
	var plan Plan

	openByUID := make(map[string]db.OpenIntervalRef, len(open))
	for _, o := range open {
		openByUID[o.PodUID] = o
	}

	seen := make(map[string]struct{}, len(pods))

	for _, pod := range pods {
		if pod.StartedAt == nil {
			// Unscheduled: the scheduler has not bound it, so no node
			// capacity is reserved for it yet.
			plan.UnscheduledPods++
			continue
		}
		ref, ok := apps[pod.Namespace+"/"+pod.AppSlug]
		if !ok {
			plan.OrphanPods++
			continue
		}
		seen[pod.UID] = struct{}{}

		_, alreadyOpen := openByUID[pod.UID]
		if !alreadyOpen {
			gpuType := (*string)(nil)
			if pod.GPUType != "" {
				t := pod.GPUType
				gpuType = &t
			}
			plan.Opens = append(plan.Opens, db.AllocationInterval{
				PodUID:        pod.UID,
				TeamID:        ref.TeamID,
				AppID:         ref.AppID,
				Namespace:     pod.Namespace,
				PodName:       pod.Name,
				CPUMillicores: pod.CPUMillicores,
				MemoryBytes:   pod.MemoryBytes,
				GPUCount:      pod.GPUCount,
				GPUType:       gpuType,
				StartedAt:     *pod.StartedAt,
				LastSeenAt:    now,
			})
		}

		// A pod can be born and die between two passes and still be
		// listed here; opening and closing it in the same plan is what
		// keeps short-lived pods on the books.
		if end, reason := authoritativeEnd(pod); end != nil {
			if end.Before(*pod.StartedAt) {
				end = pod.StartedAt
			}
			plan.Closes = append(plan.Closes, Close{
				PodUID:    pod.UID,
				EndedAt:   *end,
				Estimated: false,
				Reason:    reason,
			})
			continue
		}
		if alreadyOpen {
			plan.Touches = append(plan.Touches, pod.UID)
		}
	}

	// Anything the ledger still holds open but the cluster no longer has
	// ended at some unknown moment after we last saw it. Closing at
	// last_seen_at under-bills by at most one pass, which is the correct
	// direction to be wrong in (spec § 14 rule #3).
	for _, o := range open {
		if _, present := seen[o.PodUID]; present {
			continue
		}
		end := o.LastSeenAt
		if end.Before(o.StartedAt) {
			end = o.StartedAt
		}
		plan.Closes = append(plan.Closes, Close{
			PodUID:    o.PodUID,
			EndedAt:   end,
			Estimated: true,
			Reason:    db.CloseReasonReconciledMissing,
		})
	}

	return plan
}

// authoritativeEnd picks the strongest end time K8s offers, or nil if the
// pod is still running. Termination beats deletion: deletionTimestamp
// records when removal was *requested*, while finishedAt records when the
// containers actually stopped consuming.
func authoritativeEnd(pod k3s.PodSnapshot) (*time.Time, string) {
	if pod.TerminatedAt != nil {
		return pod.TerminatedAt, db.CloseReasonTerminated
	}
	if pod.DeletedAt != nil {
		return pod.DeletedAt, db.CloseReasonDeleted
	}
	return nil, ""
}
