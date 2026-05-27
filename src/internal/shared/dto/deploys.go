package dto

// RedeployRequest is the preview/confirm input shared by CLI, MCP, and the
// HTTP layer. Both fields are optional; backend resolves defaults from the
// app row when omitted (spec § 6.1 RedeployArgs).
type RedeployRequest struct {
	Ref       *string `json:"ref,omitempty"`
	CommitSHA *string `json:"commit_sha,omitempty"`
}

// ConfirmRedeployRequest carries the preview id produced by the redeploy
// preview endpoint. preview_id is the only field — args are persisted on
// the preview row server-side per ADR-0002 A1.
type ConfirmRedeployRequest struct {
	PreviewID string `json:"preview_id"`
}

// RedeployResponse is the durable confirm response stored in
// preview.last_result (spec § 6.1 RedeployLastResult).
type RedeployResponse struct {
	AppID         string `json:"app_id"`
	AppSlug       string `json:"app_slug"`
	DeployRunID   string `json:"deploy_run_id"`
	TraceID       string `json:"trace_id"`
	CommitSHA     string `json:"commit_sha"`
	Ref           string `json:"ref"`
	Source        string `json:"source"`         // "user" | "webhook"
	SubdomainURL  string `json:"subdomain_url"`
	InitialDeploy bool   `json:"initial_deploy"` // always false for redeploy
}
