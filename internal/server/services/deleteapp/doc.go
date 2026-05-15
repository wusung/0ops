// Package deleteapp orchestrates the delete_app preview/confirm saga
// (delete-app-flow spec).
//
// The saga is split into a reversible phase and an irreversible reconciler
// follow-up so that any failure before ArgoCD prune can be compensated:
//
//	Reversible:
//	  R1 cancel in-flight deploy_run (best-effort GHA cancelWorkflowRun)
//	  R2 unbind Cloudflare custom hostnames (kind='extra')
//	  R3 delete domain_binding rows
//	  R4 render & push 0ops-gitops manifest removal
//	  => set app.status='deleting'
//	  => enqueue cleanup_residue reconciliation_job
//
//	Irreversible (asynchronous, via cleanup_residue handler):
//	  I1 wait for ArgoCD Application to disappear (5 min timeout)
//	  I2 hard delete app row; audit_log retained permanently
//
// Compensation runs in reverse order on reversible failure; Cloudflare
// custom hostname rebind is NOT attempted in v1 — instead the partial
// release is left to cleanup_residue (spec § 5.2 Compensate).
//
// The reconciler tick itself is provided by M5.3; this package only
// supplies the per-job handler.
package deleteapp
