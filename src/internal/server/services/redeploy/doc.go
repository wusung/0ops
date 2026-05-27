// Package redeploy orchestrates the user- and webhook-initiated redeploy
// flow defined in docs/features/webhook-and-redeploy/spec.md § 6.
//
// Two entry points share the same downstream side-effect path:
//
//  1. Service.Preview / Service.Confirm — preview/confirm gate that backs the
//     CLI `0ops deploys redeploy` and MCP `redeploy_preview`/`redeploy` tools.
//     Confirm replays last_result on idempotent retry (ADR-0002 B1).
//
//  2. Trigger.Trigger — webhook-driven entry. Skips preview/confirm because
//     a push event is system-driven, not user-interactive (spec § 6.4 and
//     § 14 hard rule #6). Both paths land on the same INSERT deploy_run +
//     workflow_dispatch sequence so behavior stays consistent.
//
// Paused apps are refused at user confirm (422 app_paused) and silently
// skipped + audited at webhook entry (200 OK back to GitHub) per spec § 7.
package redeploy
