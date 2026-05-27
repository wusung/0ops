// Package reconciler implements the M5.3 reconciliation loop:
// deploy_run state-machine sweeps, ArgoCD / GitHub Actions stuck-run
// pulls, the reconciliation_job worker queue, and incident lifecycle
// management. Per spec § 5.2 every loop is leader-only — v1 ships an
// AlwaysLeader stub so the existing single-replica deployment keeps
// working until backend-ha-leader-election (M5.5) plugs in the real
// Lease-backed Leader.
package reconciler
