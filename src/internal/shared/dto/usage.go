package dto

// UsageBillingDisclaimer is attached to every usage response. The ledger
// is an allocation record, not an invoice: v1 plans are flat packages
// (ADR-0011 DD5) and no money is computed from these numbers.
const UsageBillingDisclaimer = "Allocation record for visibility only. This is not a bill and no charge is derived from it."

// Usage source markers. A closed day comes from the permanent rollup; the
// current day is integrated live from intervals and will keep growing.
const (
	UsageSourceRollup = "rollup"
	UsageSourceLive   = "live"
)

// UsageTotals is what a window cost in allocation terms.
//
// MemoryByteSeconds is a decimal string, not a number: bytes x seconds
// exceeds what a JSON number can carry exactly in a browser, and silently
// rounding a usage figure is worse than making the caller parse it.
type UsageTotals struct {
	CPUMillicoreSeconds int64  `json:"cpu_millicore_seconds"`
	MemoryByteSeconds   string `json:"memory_byte_seconds"`
	GPUCountSeconds     int64  `json:"gpu_count_seconds"`
	PodSeconds          int64  `json:"pod_seconds"`

	// EstimatedSeconds is the part of PodSeconds whose end time was not
	// observed and was conservatively assumed. Always an under-estimate.
	EstimatedSeconds int64 `json:"estimated_seconds"`
	// EstimatedRatio is EstimatedSeconds / PodSeconds, 0 when nothing was
	// estimated. Surfaced so a caller can tell a precise day from a
	// patchy one without doing the division.
	EstimatedRatio float64 `json:"estimated_ratio"`

	// Convenience renderings of the same numbers.
	CPUCoreHours   float64 `json:"cpu_core_hours"`
	MemoryGiBHours float64 `json:"memory_gib_hours"`
	GPUHours       float64 `json:"gpu_hours"`
	PodHours       float64 `json:"pod_hours"`
}

// ObservedUsage is what an app actually consumed, as distinct from what
// it reserved.
//
// Deliberately a separate struct rather than extra fields on
// UsageTotals: allocation and consumption answer different questions and
// must never be added together or mistaken for one another (ADR-0018).
// Averages describe typical load; peaks are what a limit has to cover.
//
// Only available within the sample retention window, and only when
// metrics-server is installed. Absent means "not measured", never
// "measured zero".
type ObservedUsage struct {
	CPUMillicoresAvg int   `json:"cpu_millicores_avg"`
	CPUMillicoresMax int   `json:"cpu_millicores_max"`
	MemoryBytesAvg   int64 `json:"memory_bytes_avg"`
	MemoryBytesMax   int64 `json:"memory_bytes_max"`
	SampleCount      int   `json:"sample_count"`
}

// UsageDay is one UTC day of one app's allocation.
type UsageDay struct {
	Day    string      `json:"day"` // YYYY-MM-DD, UTC
	Source string      `json:"source"`
	Totals UsageTotals `json:"totals"`

	// Observed is present only when observation data was requested and
	// exists for this day.
	Observed *ObservedUsage `json:"observed,omitempty"`
}

// AppUsage is one app's allocation over the requested window.
type AppUsage struct {
	AppID   string      `json:"app_id"`
	AppSlug string      `json:"app_slug"`
	Days    []UsageDay  `json:"days"`
	Totals  UsageTotals `json:"totals"`

	// Observed summarises consumption across the window, when asked for.
	Observed *ObservedUsage `json:"observed,omitempty"`
}

// UsageInterval is one pod's raw allocation record, returned only when a
// caller explicitly asks for interval detail.
type UsageInterval struct {
	PodUID        string  `json:"pod_uid"`
	AppID         string  `json:"app_id"`
	PodName       string  `json:"pod_name"`
	Namespace     string  `json:"namespace"`
	CPUMillicores int     `json:"cpu_millicores"`
	MemoryBytes   int64   `json:"memory_bytes"`
	GPUCount      int     `json:"gpu_count"`
	GPUType       *string `json:"gpu_type,omitempty"`
	StartedAt     string  `json:"started_at"`
	EndedAt       *string `json:"ended_at,omitempty"`
	Estimated     bool    `json:"estimated"`
	CloseReason   *string `json:"close_reason,omitempty"`
}

// UsageResponse is the wire shape for both the team and app endpoints.
type UsageResponse struct {
	From              string          `json:"from"` // YYYY-MM-DD, UTC, inclusive
	To                string          `json:"to"`   // YYYY-MM-DD, UTC, inclusive
	Apps              []AppUsage      `json:"apps"`
	Totals            UsageTotals     `json:"totals"`
	Intervals         []UsageInterval `json:"intervals,omitempty"`
	BillingDisclaimer string          `json:"billing_disclaimer"`
}
