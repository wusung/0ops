// Package leader provides backend leader election for the 0ops HA topology
// (spec docs/features/backend-ha-leader-election/spec.md; ADR-0008 § 4 #3).
//
// The package exposes a small Leader interface (IsLeader / Identity) and
// two implementations:
//
//   - AlwaysLeader — v1 / dev stub used when OPS_LEADER_MODE=always; the
//     gate is unconditionally open so dev compose and single-replica
//     deployments behave exactly like pre-M5.5.
//   - LeaseLeader — production implementation used when
//     OPS_LEADER_MODE=lease; backed by a K8s coordination/v1 Lease
//     object (k8s.io/client-go/tools/leaderelection). Lease timings are
//     fixed at 15s / 10s / 2s per spec § 4.2; ReleaseOnCancel is
//     hard-set to true per spec § 14 hard rule #4.
//
// Callers use the gate via pull: the reconciler runner already checks
// Leader.IsLeader() per tick (see internal/server/services/reconciler/
// runner.go), so this package does not push transitions through a
// channel. Lifecycle metrics flow through an Observer interface
// (OnGained / OnLost / OnNewLeader / OnLeaseRenew); production wires
// it onto observability.Metrics so the zeroops_leader_status,
// zeroops_leader_handover_total, and zeroops_leader_lease_renew_total
// series stay in sync with the Lease (spec § 8.1).
//
// The package is decoupled from cmd/server bootstrap so unit tests can
// exercise callback semantics without standing up a real K8s API
// server; full real-cluster verification of the < 5s handover SLO is
// covered by deploy/server chart guards and ops staging drills.
package leader
