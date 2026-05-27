// Package githubwebhook owns the single `POST /v1/webhooks/github`
// endpoint defined by docs/features/webhook-and-redeploy/spec.md § 4.
//
// The dispatcher is event-type aware:
//
//   - `push`              → push_handler → redeploy.Trigger (spec § 5)
//   - `installation*`     → delegated to githubapp.Service (github-app-install-flow § 7)
//   - `ping`              → ack 200, no work
//   - any other event     → ack 200, ignored (so GitHub does not retry)
//
// Every event flows through three guards before the handler runs:
//
//  1. HMAC-SHA256 verification with current + previous secret rotation
//     window (spec § 8). Reuses githubapp.WebhookVerifier so the install
//     flow and the push flow share one verifier — secret rotation lands
//     once for both.
//  2. Payload size cap of 5 MB (spec § 14 hard rule #7).
//  3. webhook_dedup (provider='github', delivery_id=X-GitHub-Delivery) for
//     24h replay protection (spec § 4.3, hard rule #3).
//
// Verification + dedup happen BEFORE any DB write or downstream call
// (spec § 14 hard rule #10).
package githubwebhook
