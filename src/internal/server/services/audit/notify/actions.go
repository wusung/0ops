package notify

// Audit actions that the notify subsystem itself writes. These are config /
// control-plane state changes, NOT delivery attempts: a delivery attempt never
// writes audit_log (spec § 7.5, hard rule #5) to avoid recursion and noise.
const (
	// ActionSubscriptionDisabled is written once when the circuit breaker
	// auto-disables a subscription after sustained delivery failure
	// (spec § 7.4). Source = system, subject = the subscription.
	ActionSubscriptionDisabled = "webhook_subscription_disabled"
	// ActionRedeliver is written when an owner/admin manually re-queues a
	// delivery (spec § 7.6). The delivery itself stays out of audit_log.
	ActionRedeliver = "webhook_redeliver"
	// ActionSubscriptionCreate / Update / Delete tag the subscription CRUD
	// config changes (spec § 7.3 / § 10). They are config writes, not
	// deliveries, so audit is correct and non-recursive.
	ActionSubscriptionCreate = "webhook_subscription_create"
	ActionSubscriptionUpdate = "webhook_subscription_update"
	ActionSubscriptionDelete = "webhook_subscription_delete"
	ActionSecretRotate       = "webhook_subscription_rotate_secret"
)

// Subject type used on the audit rows above.
const SubjectTypeSubscription = "webhook_subscription"
