package main

// Contract/E2E tests: boot the REAL router (buildRouter) against a temp
// SQLite DB and a stub agent bridge, and assert the HTTP contract the
// frontend + Python agent rely on. These lock in the exact bugs documented
// in DHUNTER_NOTES.md so they can't regress silently:
//
//   - POST /api/runs must return BOTH `id` and `run_id`
//   - POST /api/targets takes `input` (not `value`), returns `id` + `value`
//   - vulnerabilities live under /api/vulnerabilities (not /api/vulns) and
//     per-run under /runs/:id/vulnerabilities with a `vulnerabilities` key
//   - /api/runs/:id/report returns markdown
//   - SSE requires the admin token (?token= or Bearer)

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/dhunter/dhunter/internal/agent"
	"github.com/dhunter/dhunter/internal/config"
	"github.com/dhunter/dhunter/internal/db"
	"github.com/dhunter/dhunter/internal/handler"
	"github.com/dhunter/dhunter/internal/store"
	"github.com/dhunter/dhunter/internal/stream"
)

// stubBridge records calls instead of talking to a Python sidecar, so the
// contract tests exercise the real router without a live agent. Counters
// are atomic: POST /api/runs kicks off the sidecar call in a fire-and-forget
// goroutine, so the test must poll instead of asserting immediately.
type stubBridge struct {
	createRunCalls atomic.Int32
	subscribeCalls atomic.Int32
}

func (s *stubBridge) CreateRun(_ context.Context, _ agent.CreateRunRequest) error {
	s.createRunCalls.Add(1)
	return nil
}
func (s *stubBridge) CancelRun(_ context.Context, _ string) error { return nil }
func (s *stubBridge) PauseRun(_ context.Context, _ string) error  { return nil }
func (s *stubBridge) Subscribe(_ context.Context, _ string) error {
	s.subscribeCalls.Add(1)
	return nil
}

// waitBridge polls until the bridge records >= n CreateRun calls (the
// handler dispatches it asynchronously after the HTTP response).
func (e *e2eEnv) waitBridgeCreate(t *testing.T, n int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for e.bridge.createRunCalls.Load() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := e.bridge.createRunCalls.Load(); got < n {
		t.Fatalf("bridge.CreateRun calls = %d, want >= %d", got, n)
	}
}

type e2eEnv struct {
	ts       *httptest.Server
	token    string
	stores   *store.Stores
	bridge   *stubBridge
	hub      *stream.Hub
	database *db.DB
}

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "dhunter.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = database.Close(ctx)
	})
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	stores := store.New(database)
	cfg := config.Default()
	cfg.Admin.Token = "e2e-test-token"
	cfg.Admin.BootstrapPassword = "e2e-pass" // known password → handler gets its hash
	cfg.Storage.SQLitePath = dir             // unused by router, keeps cfg sane
	cfg.Server.Port = 0

	adminUser, passwordHash, _, err := bootstrapAdmin(cfg, stores.Settings)
	if err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}

	hub := stream.New()
	bridge := &stubBridge{}
	router := buildRouter(cfg, stores, hub, bridge, adminUser, passwordHash)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	return &e2eEnv{ts: ts, token: cfg.Admin.Token, stores: stores, bridge: bridge, hub: hub, database: database}
}

func (e *e2eEnv) do(t *testing.T, method, path, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (e *e2eEnv) mustJSON(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	resp, raw := e.do(t, method, path, token, body)
	var m map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s %s: response not JSON: %s", method, path, raw)
		}
	}
	return resp.StatusCode, m
}

// --- health + auth --------------------------------------------------------

func TestHealthzIsPublic(t *testing.T) {
	e := newE2E(t)
	resp, raw := e.do(t, "GET", "/api/healthz", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("healthz = %d (%s)", resp.StatusCode, raw)
	}
}

func TestAPIAuthRequired(t *testing.T) {
	e := newE2E(t)
	resp, _ := e.do(t, "GET", "/api/targets", "", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("GET /api/targets without token = %d, want 401", resp.StatusCode)
	}
	// Query-param token form must work too (frontend SSE uses it).
	resp, _ = e.do(t, "GET", "/api/targets?token="+e.token, "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/targets?token=... = %d, want 200", resp.StatusCode)
	}
}

func TestLoginReturnsToken(t *testing.T) {
	e := newE2E(t)
	// newE2E bootstrapped admin/e2e-pass, so the handler holds that hash.
	code, m := e.mustJSON(t, "POST", "/api/auth/login", "", map[string]string{"username": "admin", "password": "wrong"})
	if code != 401 {
		t.Fatalf("login wrong password = %d, want 401", code)
	}
	code, m = e.mustJSON(t, "POST", "/api/auth/login", "", map[string]string{"username": "admin", "password": "e2e-pass"})
	if code != 200 {
		t.Fatalf("login correct password = %d (%v), want 200", code, m)
	}
	if tok, _ := m["token"].(string); tok != e.token {
		t.Fatalf("login token = %q, want %q", tok, e.token)
	}
}

// --- target contract ------------------------------------------------------

func TestTargetContract(t *testing.T) {
	e := newE2E(t)
	// POST takes `input` (NOT `value` — the old frontend bug) and returns id+value.
	code, tgt := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "example.com", "type": "auto"})
	if code != 201 {
		t.Fatalf("POST /api/targets = %d (%v), want 201", code, tgt)
	}
	if id, _ := tgt["id"].(string); id == "" {
		t.Fatalf("target response missing id: %v", tgt)
	}
	if v, _ := tgt["value"].(string); v != "example.com" {
		t.Fatalf("target value = %q, want example.com", v)
	}
	if ty, _ := tgt["type"].(string); ty != "domain" {
		t.Fatalf("target type = %q, want domain", ty)
	}

	code, list := e.mustJSON(t, "GET", "/api/targets", e.token, nil)
	if code != 200 {
		t.Fatalf("GET /api/targets = %d, want 200", code)
	}
	arr, ok := list["targets"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("GET /api/targets targets = %v, want 1 item", list["targets"])
	}
	first := arr[0].(map[string]any)
	if _, ok := first["id"]; !ok {
		t.Fatalf("target list item missing id: %v", first)
	}
	if _, ok := first["value"]; !ok {
		t.Fatalf("target list item missing value: %v", first)
	}
}

// --- run + vulnerability + report contract --------------------------------

func TestRunVulnReportContract(t *testing.T) {
	e := newE2E(t)
	code, tgt := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "juice.example.com", "type": "auto"})
	if code != 201 {
		t.Fatalf("create target = %d (%v)", code, tgt)
	}
	targetID := tgt["id"].(string)

	// POST /api/runs must return BOTH id and run_id (old clients use run_id).
	code, run := e.mustJSON(t, "POST", "/api/runs", e.token, map[string]any{"target_id": targetID, "objective": "e2e"})
	if code != 202 {
		t.Fatalf("POST /api/runs = %d (%v), want 202", code, run)
	}
	id, _ := run["id"].(string)
	runID, _ := run["run_id"].(string)
	if id == "" || runID != id {
		t.Fatalf("POST /api/runs must return id==run_id, got %v", run)
	}
	// The sidecar call is fire-and-forget — wait for it.
	e.waitBridgeCreate(t, 1)

	// GET /api/runs/:id returns the run with `id` and the denormalized
	// target name (so list views show which target was tested).
	code, got := e.mustJSON(t, "GET", "/api/runs/"+id, e.token, nil)
	if code != 200 || got["id"] != id {
		t.Fatalf("GET /api/runs/:id = %d (%v)", code, got)
	}
	if tv, _ := got["target_value"].(string); tv != "juice.example.com" {
		t.Fatalf("run target_value = %q, want juice.example.com (LEFT JOIN must populate it)", tv)
	}

	// POST /api/vulnerabilities (flat write_finding shape) → pending.
	code, vuln := e.mustJSON(t, "POST", "/api/vulnerabilities", e.token, map[string]any{
		"run_id": runID, "title": "SQLi in login", "severity": "critical",
		"target": "https://juice.example.com/login", "evidence": "curl ...",
	})
	if code != 201 {
		t.Fatalf("POST /api/vulnerabilities = %d (%v), want 201", code, vuln)
	}
	if st, _ := vuln["status"].(string); st != "pending" {
		t.Fatalf("new finding status = %q, want pending", st)
	}
	vulnID := vuln["id"].(string)

	// PATCH flips lifecycle (verifier action).
	code, _ = e.mustJSON(t, "PATCH", "/api/vulnerabilities/"+vulnID, e.token, map[string]any{"status": "confirmed"})
	if code != 200 {
		t.Fatalf("PATCH vuln = %d, want 200", code)
	}

	// Per-run list endpoint: /runs/:id/vulnerabilities with `vulnerabilities` key.
	code, list := e.mustJSON(t, "GET", "/api/runs/"+id+"/vulnerabilities", e.token, nil)
	if code != 200 {
		t.Fatalf("GET /runs/:id/vulnerabilities = %d", code)
	}
	arr, ok := list["vulnerabilities"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("run vulns = %v, want 1", list["vulnerabilities"])
	}
	if st := arr[0].(map[string]any)["status"]; st != "confirmed" {
		t.Fatalf("vuln status after PATCH = %v, want confirmed", st)
	}

	// Global list endpoint: /api/vulnerabilities (NOT /api/vulns).
	code, g := e.mustJSON(t, "GET", "/api/vulnerabilities?run_id="+runID, e.token, nil)
	if code != 200 {
		t.Fatalf("GET /api/vulnerabilities = %d", code)
	}
	if arr, ok := g["vulnerabilities"].([]any); !ok || len(arr) != 1 {
		t.Fatalf("global vulns = %v, want 1", g["vulnerabilities"])
	}

	// Report: markdown under /runs/:id/report.
	resp, raw := e.do(t, "GET", "/api/runs/"+id+"/report", e.token, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /runs/:id/report = %d (%s)", resp.StatusCode, raw)
	}
	if !bytes.Contains(raw, []byte("# Dhunter 渗透测试报告")) {
		t.Fatalf("report is not the expected markdown: %s", raw[:minInt2(len(raw), 200)])
	}

	// Tool calls list contract (UI polls it).
	code, tc := e.mustJSON(t, "GET", "/api/runs/"+id+"/tool_calls", e.token, nil)
	if code != 200 {
		t.Fatalf("GET /runs/:id/tool_calls = %d", code)
	}
	if _, ok := tc["tool_calls"]; !ok {
		t.Fatalf("tool_calls response missing `tool_calls` key: %v", tc)
	}
}

func TestProjectRunsContract(t *testing.T) {
	e := newE2E(t)
	code, tgt := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "proj.example.com", "type": "auto"})
	if code != 201 {
		t.Fatalf("create target = %d", code)
	}
	targetID := tgt["id"].(string)
	code, run := e.mustJSON(t, "POST", "/api/runs", e.token, map[string]any{"target_id": targetID, "objective": "x"})
	if code != 202 {
		t.Fatalf("create run = %d", code)
	}
	code, list := e.mustJSON(t, "GET", "/api/targets/"+targetID+"/runs", e.token, nil)
	if code != 200 {
		t.Fatalf("GET /targets/:id/runs = %d", code)
	}
	if arr, ok := list["runs"].([]any); !ok || len(arr) != 1 {
		t.Fatalf("project runs = %v, want 1 (got run %v)", list["runs"], run)
	}
}

// --- SSE auth -------------------------------------------------------------

func TestSSERequiresToken(t *testing.T) {
	e := newE2E(t)
	code, tgt := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "sse.example.com", "type": "auto"})
	if code != 201 {
		t.Fatalf("create target = %d", code)
	}
	_, run := e.mustJSON(t, "POST", "/api/runs", e.token, map[string]any{"target_id": tgt["id"], "objective": "x"})
	runID := run["run_id"].(string)

	// Without a token the SSE stream must be rejected (NOT stream the run).
	resp, _ := e.do(t, "GET", "/api/runs/"+runID+"/events", "", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("SSE without token = %d, want 401", resp.StatusCode)
	}

	// With ?token= the stream opens and emits the initial `open` event.
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/runs/"+runID+"/events?token="+e.token, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE with token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("SSE with token = %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	if !bytes.Contains([]byte(body), []byte("event: open")) {
		t.Fatalf("SSE did not emit initial open event: %q", body)
	}
}

// --- login rate limit -----------------------------------------------------

func TestLoginRateLimited(t *testing.T) {
	e := newE2E(t)
	// 10 allowed, then 429. Use a fresh server per IP bucket — httptest
	// ClientIP is 127.0.0.1 for all requests, which is exactly the case the
	// limiter must protect against (local spray / single exposed host).
	var last int
	for i := 0; i < 12; i++ {
		code, _ := e.mustJSON(t, "POST", "/api/auth/login", "", map[string]string{"username": "admin", "password": "wrong"})
		last = code
	}
	if last != 429 {
		t.Fatalf("login after 12 attempts = %d, want 429", last)
	}
}

// --- token resolution -----------------------------------------------------

func TestResolveAdminTokenPersists(t *testing.T) {
	e := newE2E(t)
	// A configured token wins.
	if got := resolveAdminToken(e.stores.Settings, "cfg-token"); got != "cfg-token" {
		t.Fatalf("configured token not used: %q", got)
	}
	// Empty configured → generated + persisted (stable across calls).
	first := resolveAdminToken(e.stores.Settings, "")
	if len(first) < 16 {
		t.Fatalf("generated token too short: %q", first)
	}
	second := resolveAdminToken(e.stores.Settings, "")
	if first != second {
		t.Fatalf("persisted token not stable: %q vs %q", first, second)
	}
	if first == "dhunter-admin-please-change-me" {
		t.Fatalf("static default token leaked into resolution")
	}
}

// --- config: no static default token --------------------------------------

func TestConfigHasNoStaticDefaultToken(t *testing.T) {
	cfg := config.Default()
	if cfg.Admin.Token != "" {
		t.Fatalf("Default() admin token = %q, want empty (generate at boot)", cfg.Admin.Token)
	}
	if cfg.MCP.WebHunter.Token != "" {
		t.Fatalf("Default() MCP token = %q, want empty", cfg.MCP.WebHunter.Token)
	}
	if cfg.Server.Port != 13343 {
		t.Fatalf("Default() port = %d, want 13343", cfg.Server.Port)
	}
}

// --- cross-run vulnerability dedup ----------------------------------------

func TestCrossRunVulnDedup(t *testing.T) {
	// Re-running a target must not pile up identical findings: the same
	// title+target for the same target from an EARLIER run is a duplicate
	// (409 + existing_id), while genuinely new findings still insert.
	e := newE2E(t)
	code, tgt := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "dup.example.com", "type": "auto"})
	if code != 201 {
		t.Fatalf("create target = %d", code)
	}
	targetID := tgt["id"].(string)
	_, run1 := e.mustJSON(t, "POST", "/api/runs", e.token, map[string]any{"target_id": targetID, "objective": "x"})
	_, run2 := e.mustJSON(t, "POST", "/api/runs", e.token, map[string]any{"target_id": targetID, "objective": "y"})
	run1ID := run1["run_id"].(string)
	run2ID := run2["run_id"].(string)

	// Finding in run1, using the Python fallback shape (no target_id — the
	// handler must fall back to the run's target for cross-run dedup).
	code, v1 := e.mustJSON(t, "POST", "/api/vulnerabilities", e.token, map[string]any{
		"run_id": run1ID, "title": "SQL injection in login", "severity": "high", "target": "https://dup.example.com/login",
	})
	if code != 201 {
		t.Fatalf("first finding = %d (%v), want 201", code, v1)
	}

	// Same finding in run2 → 409 pointing at the existing row.
	code, dup := e.mustJSON(t, "POST", "/api/vulnerabilities", e.token, map[string]any{
		"run_id": run2ID, "title": "SQL injection in login", "severity": "high", "target": "https://dup.example.com/login",
	})
	if code != 409 {
		t.Fatalf("cross-run duplicate = %d (%v), want 409", code, dup)
	}
	if dup["existing_id"] != v1["id"] {
		t.Fatalf("existing_id = %v, want %v", dup["existing_id"], v1["id"])
	}

	// A genuinely different finding in run2 still inserts.
	code, _ = e.mustJSON(t, "POST", "/api/vulnerabilities", e.token, map[string]any{
		"run_id": run2ID, "title": "XSS in profile", "severity": "medium", "target": "https://dup.example.com/profile",
	})
	if code != 201 {
		t.Fatalf("new finding = %d, want 201", code)
	}

	// The per-run lists stay independent (run2 has exactly the XSS).
	_, list2 := e.mustJSON(t, "GET", "/api/runs/"+run2ID+"/vulnerabilities", e.token, nil)
	arr := list2["vulnerabilities"].([]any)
	if len(arr) != 1 || arr[0].(map[string]any)["title"] != "XSS in profile" {
		t.Fatalf("run2 vulns = %v, want only the XSS", arr)
	}
}

// --- two-account A/B IDOR contract ----------------------------------------

func TestTargetAuthAccountFields(t *testing.T) {
	// The frontend sends account_a/account_b on PATCH /targets/:id/auth;
	// the backend must persist them verbatim (the Python agent reads them
	// for auto-login + A/B session switching). This test locks in the
	// contract that was silently dropped before (fields missing from the
	// Go request struct).
	e := newE2E(t)
	code, tgt := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "idor.example.com", "type": "auto"})
	if code != 201 {
		t.Fatalf("create target = %d", code)
	}
	targetID := tgt["id"].(string)

	code, _ = e.mustJSON(t, "PATCH", "/api/targets/"+targetID+"/auth", e.token, map[string]any{
		"cookies":   "SESS=top-level",
		"account_a": map[string]any{"username": "userA", "password": "pwA", "login_url": "https://idor.example.com/login", "cookies": "SESS=a"},
		"account_b": map[string]any{"username": "userB", "password": "pwB", "cookies": "SESS=b"},
	})
	if code != 200 {
		t.Fatalf("PATCH auth = %d, want 200", code)
	}

	code, got := e.mustJSON(t, "GET", "/api/targets/"+targetID, e.token, nil)
	if code != 200 {
		t.Fatalf("GET target = %d", code)
	}
	raw, _ := got["auth_context"].(string)
	if raw == "" {
		t.Fatalf("auth_context empty after PATCH")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("auth_context not JSON: %q (%v)", raw, err)
	}
	if parsed["cookies"] != "SESS=top-level" {
		t.Fatalf("top-level cookies lost: %v", parsed)
	}
	aa, ok := parsed["account_a"].(map[string]any)
	if !ok || aa["username"] != "userA" || aa["cookies"] != "SESS=a" || aa["login_url"] != "https://idor.example.com/login" {
		t.Fatalf("account_a not persisted: %v", parsed)
	}
	ab, ok := parsed["account_b"].(map[string]any)
	if !ok || ab["username"] != "userB" || ab["cookies"] != "SESS=b" {
		t.Fatalf("account_b not persisted: %v", parsed)
	}
}

// --- SSE wire contract ----------------------------------------------------

func TestSSECarriesTypeAndCallID(t *testing.T) {
	// The bridge publishes events with `type` (+ legacy `event_type`) and
	// pairs tool_call/tool_result via call_id. Publish a tool_call through
	// the real hub and assert the SSE stream the browser consumes carries
	// those fields — this is what the frontend's live-stream panes rely on.
	e := newE2E(t)
	code, tgt := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "sse2.example.com", "type": "auto"})
	if code != 201 {
		t.Fatalf("create target = %d", code)
	}
	_, run := e.mustJSON(t, "POST", "/api/runs", e.token, map[string]any{"target_id": tgt["id"], "objective": "x"})
	runID := run["run_id"].(string)

	req, _ := http.NewRequest("GET", e.ts.URL+"/api/runs/"+runID+"/events?token="+e.token, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("SSE status = %d", resp.StatusCode)
	}

	// Block until the initial `open` event arrives — only then is the
	// subscription live and a subsequent hub.Publish guaranteed to be
	// relayed (the hub drops events with no subscriber).
	openBuf := make([]byte, 512)
	if n, rerr := resp.Body.Read(openBuf); n == 0 || !bytes.Contains(openBuf[:n], []byte("event: open")) {
		t.Fatalf("expected initial open event, got %q (err=%v)", openBuf[:n], rerr)
	}

	// Publish a tool_call exactly as the bridge does for a live run.
	ev := &agent.Event{EventType: "tool_call", Type: "tool_call", RunID: runID, Name: "http_request", CallID: "call_abc"}
	raw, _ := json.Marshal(ev)
	e.hub.Publish(runID, raw)

	bodyCh := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if strings.Contains(sb.String(), `"call_id":"call_abc"`) {
					bodyCh <- sb.String()
					return
				}
			}
			if rerr != nil {
				bodyCh <- sb.String()
				return
			}
		}
	}()
	select {
	case body := <-bodyCh:
		if !strings.Contains(body, `"type":"tool_call"`) {
			t.Fatalf("SSE event missing `type`: %s", body)
		}
		if !strings.Contains(body, `"event_type":"tool_call"`) {
			t.Fatalf("SSE event missing legacy `event_type`: %s", body)
		}
		if !strings.Contains(body, `"call_id":"call_abc"`) {
			t.Fatalf("SSE event missing `call_id`: %s", body)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for the published tool_call event on the SSE stream")
	}
}

// --- admin password recovery ----------------------------------------------

func TestForceResetPassword(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()

	// force_reset_password + explicit bootstrap_password overwrites the
	// persisted hash (the lost-password recovery path).
	cfg := config.Default()
	cfg.Admin.Username = "admin"
	cfg.Admin.BootstrapPassword = "reset-pass-1"
	cfg.Admin.ForceResetPassword = true
	user, hash, _, err := bootstrapAdmin(cfg, e.stores.Settings)
	if err != nil {
		t.Fatalf("force reset: %v", err)
	}
	if user != "admin" {
		t.Fatalf("force reset username = %q, want admin", user)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("reset-pass-1")) != nil {
		t.Fatal("force-reset hash does not match the new password")
	}
	persisted, _ := e.stores.Settings.Get(ctx, handler.KeyAdminPasswordHash)
	if persisted != hash {
		t.Fatal("force-reset hash not persisted")
	}

	// Next boot WITHOUT the flag reuses the persisted hash — the reset is
	// one-shot, not a permanent override.
	cfg2 := config.Default()
	cfg2.Admin.Username = "admin"
	cfg2.Admin.BootstrapPassword = "ignored-on-restart"
	_, hash2, _, err := bootstrapAdmin(cfg2, e.stores.Settings)
	if err != nil {
		t.Fatalf("reboot bootstrap: %v", err)
	}
	if hash2 != hash {
		t.Fatal("persisted hash was clobbered after force reset")
	}

	// force without an explicit password fails fast (no silent rotation).
	cfg3 := config.Default()
	cfg3.Admin.ForceResetPassword = true
	cfg3.Admin.BootstrapPassword = ""
	if _, _, _, err := bootstrapAdmin(cfg3, e.stores.Settings); err == nil {
		t.Fatal("force_reset without bootstrap_password must fail, got nil error")
	}
}

// --- helpers --------------------------------------------------------------

func minInt2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- target favorite (pinning) -------------------------------------------

func TestTargetFavoriteContract(t *testing.T) {
	// PATCH /targets/:id/favorite pins a target; List returns favorites
	// first, and the JSON carries `favorite`.
	e := newE2E(t)
	_, t1 := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "older.example.com", "type": "auto"})
	// created_at is millisecond-precision; make sure t2 is strictly newer so
	// the unpinned ordering is deterministic.
	time.Sleep(5 * time.Millisecond)
	_, t2 := e.mustJSON(t, "POST", "/api/targets", e.token, map[string]any{"input": "newer.example.com", "type": "auto"})

	// Pin the OLDER one — it must sort before the newer one.
	code, fav := e.mustJSON(t, "PATCH", "/api/targets/"+t1["id"].(string)+"/favorite", e.token, map[string]any{"favorite": true})
	if code != 200 || fav["favorite"] != true {
		t.Fatalf("PATCH favorite = %d (%v)", code, fav)
	}

	code, list := e.mustJSON(t, "GET", "/api/targets", e.token, nil)
	if code != 200 {
		t.Fatalf("GET /api/targets = %d", code)
	}
	arr := list["targets"].([]any)
	if len(arr) != 2 {
		t.Fatalf("targets = %d, want 2", len(arr))
	}
	if arr[0].(map[string]any)["id"] != t1["id"] {
		t.Fatalf("favorite target not sorted first: %v", arr[0])
	}
	if arr[0].(map[string]any)["favorite"] != true {
		t.Fatalf("favorite flag missing in list item: %v", arr[0])
	}

	// Unpin → order flips back to newest first.
	code, _ = e.mustJSON(t, "PATCH", "/api/targets/"+t1["id"].(string)+"/favorite", e.token, map[string]any{"favorite": false})
	if code != 200 {
		t.Fatalf("unpin = %d", code)
	}
	_, list = e.mustJSON(t, "GET", "/api/targets", e.token, nil)
	arr = list["targets"].([]any)
	if arr[0].(map[string]any)["id"] != t2["id"] {
		t.Fatalf("after unpin newest should sort first: %v", arr[0])
	}
}
