// Package audit implements the M5.2 audit_log write, query, and
// retention surface described in docs/features/audit-log/spec.md.
//
// The package separates concerns by file:
//
//	log.go        — Log(ctx, Entry) write path with redaction + trace_id pull
//	query.go      — Query(filter) read path with team/RBAC filtering and cursor pagination
//	partition.go  — month-partition rollover helpers (spec § 9.1)
//	redact.go     — redactor for args / result fields (spec § 8; error-model § 9 contract)
//	metrics.go    — Prometheus counters/histograms recording write + query outcomes
//
// Audit_log is a partitioned ledger: writes always succeed when the
// caller targets a covered partition. The audit-rollover job is
// expected to ensure the next 3 months of partitions are present;
// EnsurePartition lets a backend node provision the partition itself
// when the cron has not yet run.
package audit
