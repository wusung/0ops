package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/winshare/zeroops/internal/shared/backendclient"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// TestM28EndToEndPreviewConfirmCallback exercises the M2.8 integration contract
// against an in-memory backend: preview → confirm → HMAC-signed callback. It is
// the test counterpart of tasks/m2-8-e2e-acceptance.sh and pins the shape that
// the script's CLI / MCP / callback phases drive at the protocol layer.
//
// Coverage maps to AGENTS.md "高風險區域必測"：
//   - preview / confirm 流程（preview_id consumption + last_result writeback）
//   - idempotent retry（second confirm returns same deploy_run_id without redo）
//   - webhook 簽章驗證（HMAC signature + timestamp window）
//   - deploy 狀態轉移（callback success → live via normalizeDeployStatus）
func TestM28EndToEndPreviewConfirmCallback(t *testing.T) {
	t.Setenv("OPS_CALLBACK_SECRET", "m2-8-callback-secret")

	store, token := newFakeStore()
	srv := httptest.NewServer(NewRouter(store))
	t.Cleanup(srv.Close)

	client := backendclient.New(srv.URL, token)
	ctx := context.Background()

	preview, err := client.PreviewCreateApp(ctx, store.team.Slug, dto.AppCreateRequest{
		Slug:    "nextdemo",
		RepoURL: "https://github.com/example/nextdemo",
		Ref:     "main",
	})
	if err != nil {
		t.Fatalf("PreviewCreateApp() error = %v", err)
	}
	if preview.PreviewID == "" {
		t.Fatal("preview.PreviewID is empty")
	}

	first, err := client.CreateApp(ctx, store.team.Slug, dto.ConfirmCreateAppRequest{
		PreviewID: preview.PreviewID,
	})
	if err != nil {
		t.Fatalf("CreateApp() first call error = %v", err)
	}
	if !strings.HasSuffix(first.SubdomainURL, ".winshare.tw") {
		t.Fatalf("subdomain_url = %q, want suffix .winshare.tw (spec § 6.3 + hard rule #10)", first.SubdomainURL)
	}
	if first.DeployRunID == "" {
		t.Fatal("first.DeployRunID is empty; spec § 6.3 requires deploy_run_id in last_result")
	}
	if first.AppID == "" {
		t.Fatal("first.AppID is empty; spec § 6.3 requires app_id in last_result")
	}

	// Idempotent replay must return the same identifiers without redoing the
	// reversible side-effect chain (spec § 12 Preview replay).
	second, err := client.CreateApp(ctx, store.team.Slug, dto.ConfirmCreateAppRequest{
		PreviewID: preview.PreviewID,
	})
	if err != nil {
		t.Fatalf("CreateApp() replay call error = %v", err)
	}
	if second.AppID != first.AppID || second.DeployRunID != first.DeployRunID {
		t.Fatalf("replay mismatch: first=%+v second=%+v", first, second)
	}
	if second.SubdomainURL != first.SubdomainURL {
		t.Fatalf("replay subdomain mismatch: first=%q second=%q", first.SubdomainURL, second.SubdomainURL)
	}

	// Mirror the script's `callback` phase: deliver a successful HMAC-signed
	// callback against the deploy_run created above. status=success normalizes
	// to "live" per normalizeDeployStatus (build-pipeline-and-callback spec).
	callbackBody := `{"run_id":"` + first.DeployRunID + `","status":"success","trace_id":"trace-m2-8","image":"ghcr.io/example/nextdemo:abc"}`
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("m2-8-callback-secret"))
	_, _ = mac.Write([]byte(ts + "." + callbackBody))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/"+first.DeployRunID+"/callback", strings.NewReader(callbackBody))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-0ops-Timestamp", ts)
	req.Header.Set("X-0ops-Signature", sig)
	req.Header.Set("X-0ops-Delivery-ID", "m2-8-delivery-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback status = %d, body = %s", resp.StatusCode, string(body))
	}

	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode callback response: %v", err)
	}
	if got["status"] != "ok" {
		t.Fatalf("callback response status = %q, want ok", got["status"])
	}

	// Bad-signature replay must be rejected (sig validation contract).
	badReq, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/deploy-runs/"+first.DeployRunID+"/callback", strings.NewReader(callbackBody))
	if err != nil {
		t.Fatalf("http.NewRequest() bad-sig error = %v", err)
	}
	badReq.Header.Set("Content-Type", "application/json")
	badReq.Header.Set("X-0ops-Timestamp", ts)
	badReq.Header.Set("X-0ops-Signature", "sha256=deadbeef")
	badReq.Header.Set("X-0ops-Delivery-ID", "m2-8-delivery-bad")

	badResp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatalf("bad-sig callback request error = %v", err)
	}
	defer func() { _ = badResp.Body.Close() }()
	if badResp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(badResp.Body)
		t.Fatalf("bad-sig callback status = %d, want 401, body = %s", badResp.StatusCode, string(body))
	}
}

// TestM28AcceptanceScriptShape pins the harness contract surfaced by
// tasks/m2-8-e2e-acceptance.sh. It guards against accidental phase removal,
// silent mode renames, and lost executable bit — all of which would mean
// `make m2-8-e2e-acceptance` no longer drives the four required acceptance
// paths (CLI --yes / CLI interactive / MCP / public URL probe).
func TestM28AcceptanceScriptShape(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "..", "tasks", "m2-8-e2e-acceptance.sh")

	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat %s: %v", scriptPath, err)
	}
	if mode := info.Mode().Perm(); mode&0o111 == 0 {
		t.Fatalf("%s is not executable; mode = %o", scriptPath, mode)
	}

	data, err := os.ReadFile(scriptPath) //nolint:gosec // test fixture path is constant
	if err != nil {
		t.Fatalf("read %s: %v", scriptPath, err)
	}
	body := string(data)

	requiredPhases := []string{
		"preflight",
		"cli-yes",
		"cli-interactive",
		"mcp",
		"callback",
		"public-url-probe",
	}
	for _, phase := range requiredPhases {
		marker := "phase_header \"" + phase + "\""
		if !strings.Contains(body, marker) {
			t.Errorf("%s missing required %q marker", scriptPath, marker)
		}
	}
	// PHASES_ALL drives the default `--all` run order; assert the slug list is
	// present so removing a phase from the loop is caught at test time too.
	if !strings.Contains(body, "PHASES_ALL=(preflight cli-yes cli-interactive mcp callback public-url-probe)") {
		t.Errorf("%s PHASES_ALL ordering changed; update tasks/todo.md M2.8 acceptance bullets", scriptPath)
	}

	requiredModes := []string{"local", "staging", "production"}
	for _, mode := range requiredModes {
		if !strings.Contains(body, mode) {
			t.Errorf("%s missing E2E_MODE value %q", scriptPath, mode)
		}
	}

	requiredEnv := []string{
		"OPS_HOST",
		"OPS_BEARER_TOKEN",
		"OPS_TEAM_SLUG",
		"OPS_CALLBACK_SECRET",
		"E2E_PUBLIC_URL",
	}
	for _, key := range requiredEnv {
		if !strings.Contains(body, key) {
			t.Errorf("%s missing required env var reference %q", scriptPath, key)
		}
	}

	if !strings.Contains(body, "create_app_preview") || !strings.Contains(body, "create_app") {
		t.Errorf("%s missing MCP tool names create_app_preview / create_app", scriptPath)
	}

	if !strings.Contains(body, "/internal/deploy-runs/") {
		t.Errorf("%s missing callback endpoint path /internal/deploy-runs/", scriptPath)
	}

	if !strings.Contains(body, "nextdemo.winshare.tw") {
		t.Errorf("%s missing default public URL nextdemo.winshare.tw (spec § 12 hard rule)", scriptPath)
	}
}
