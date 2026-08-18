package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/db"
	"github.com/dhunter/dhunter/internal/store"
)

// newMCPTestEnv builds a fresh in-memory-ish DB + Stores. We can't use
// :memory: because the schema migration needs a real file for PRAGMA
// table_info; tempfile in t.TempDir() is fine.
func newMCPTestEnv(t *testing.T) (*store.Stores, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tmp := t.TempDir() + "/test.db"
	d, err := db.Open(tmp)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stores := store.New(d)
	return stores, func() { _ = d.Close(context.Background()) }
}

func TestMCPHandler_CRUD_Roundtrip(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))

	// 1. CREATE
	body := createMCPReq{Name: "nuclei", URL: "http://127.0.0.1:9999/mcp", Transport: "http", Token: "secret-token", Description: "nuclei scanner"}
	w := doJSON(r, "POST", "/mcp-servers", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created store.MCPServer
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Name != "nuclei" {
		t.Fatalf("bad create response: %+v", created)
	}
	if created.AuthHeader != "Authorization" || created.AuthScheme != "Bearer" {
		t.Fatalf("default auth config is wrong: header=%q scheme=%q", created.AuthHeader, created.AuthScheme)
	}
	// Token is returned on create so the user can save it client-side.
	// We can't decode into MCPServer directly because the struct tags
	// the field as `json:"-"` for redaction — peek into the raw map
	// instead, and also assert HasToken.
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if raw["token"] != "secret-token" {
		t.Fatalf("token should be present on create, got %v", raw["token"])
	}
	if !created.HasToken {
		t.Fatalf("HasToken should be true on create")
	}

	// 2. LIST — token must be redacted.
	w = doJSON(r, "GET", "/mcp-servers", nil)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Servers []*store.MCPServer `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Servers) != 1 {
		t.Fatalf("list count: want 1, got %d", len(listResp.Servers))
	}
	if listResp.Servers[0].Token != "" {
		t.Fatalf("list should redact token, got %q", listResp.Servers[0].Token)
	}
	if !listResp.Servers[0].HasToken {
		t.Fatalf("HasToken should still be true after redaction")
	}

	// 3. UPDATE — change description, keep token (do NOT send token).
	upd := updateMCPReq{Name: "nuclei-v2", URL: "http://127.0.0.1:9999/mcp", Transport: "http", Description: "updated"}
	w = doJSON(r, "PUT", "/mcp-servers/"+created.ID, upd)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	// Verify the token was NOT wiped: read raw store row.
	got, err := stores.MCPServers.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("post-update get: %v", err)
	}
	if got.Description != "updated" {
		t.Fatalf("description not updated: %q", got.Description)
	}
	if got.Name != "nuclei-v2" {
		t.Fatalf("name not updated: %q", got.Name)
	}
	if got.Token != "secret-token" {
		t.Fatalf("token wiped on update: %q", got.Token)
	}
	if got.AuthHeader != "Authorization" || got.AuthScheme != "Bearer" {
		t.Fatalf("auth config changed on update: header=%q scheme=%q", got.AuthHeader, got.AuthScheme)
	}

	// 4. UPDATE — explicitly clear token.
	clr := updateMCPReq{Name: "nuclei-v2", URL: "http://127.0.0.1:9999/mcp", Transport: "http", ClearToken: true}
	w = doJSON(r, "PUT", "/mcp-servers/"+created.ID, clr)
	if w.Code != 200 {
		t.Fatalf("update-clear: %d %s", w.Code, w.Body.String())
	}
	got, _ = stores.MCPServers.Get(context.Background(), created.ID)
	if got.HasToken {
		t.Fatalf("HasToken should be false after ClearToken")
	}

	// 5. DELETE
	w = doJSON(r, "DELETE", "/mcp-servers/"+created.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := stores.MCPServers.Get(context.Background(), created.ID); err == nil {
		t.Fatalf("expected ErrNotFound after delete")
	}
}

func TestMCPHandler_RejectsBadInput(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))

	cases := []struct {
		name string
		body createMCPReq
		want int
	}{
		{"missing name", createMCPReq{URL: "http://x", Transport: "http"}, 400},
		{"bad name chars", createMCPReq{Name: "bad name!", URL: "http://x", Transport: "http"}, 400},
		{"missing url", createMCPReq{Name: "ok", URL: "", Transport: "http"}, 400},
		{"non-http url", createMCPReq{Name: "ok", URL: "ftp://x", Transport: "http"}, 400},
		{"bad transport", createMCPReq{Name: "ok", URL: "http://x", Transport: "stdio"}, 400},
		{"bad auth header", createMCPReq{Name: "ok", URL: "http://x", Transport: "http", AuthHeader: strPtr("bad header")}, 400},
		{"reserved auth header", createMCPReq{Name: "ok", URL: "http://x", Transport: "http", AuthHeader: strPtr("Mcp-Session-Id")}, 400},
		{"bad auth scheme", createMCPReq{Name: "ok", URL: "http://x", Transport: "http", AuthScheme: strPtr("two words")}, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(r, "POST", "/mcp-servers", tc.body)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestMCPHandler_NameUniqueness(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))

	body := createMCPReq{Name: "dup", URL: "http://x", Transport: "http"}
	w := doJSON(r, "POST", "/mcp-servers", body)
	if w.Code != 201 {
		t.Fatalf("first create: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, "POST", "/mcp-servers", body)
	if w.Code != 409 {
		t.Fatalf("second create: want 409, got %d %s", w.Code, w.Body.String())
	}
}

func TestMCPHandler_TestEndpoint_ReportsTools(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()

	// Mock MCP server — speaks streamable-HTTP (one POST per request).
	var initCount, listCount int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		method, _ := req["method"].(string)
		id, _ := req["id"].(float64)
		switch method {
		case "initialize":
			atomic.AddInt32(&initCount, 1)
			writeJSONRPC(w, int(id), map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "mock", "version": "1.0.0"},
			})
		case "tools/list":
			atomic.AddInt32(&listCount, 1)
			writeJSONRPC(w, int(id), map[string]any{
				"tools": []map[string]any{
					{"name": "tool_a", "description": "first"},
					{"name": "tool_b", "description": "second"},
				},
			})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer mock.Close()

	// Create a server pointing at the mock.
	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	createBody := createMCPReq{
		Name: "mock", URL: mock.URL, Transport: "http", Token: "tok", Enabled: boolPtr(true),
	}
	w := doJSON(r, "POST", "/mcp-servers", createBody)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created store.MCPServer
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Hit /test
	w = doJSON(r, "POST", "/mcp-servers/"+created.ID+"/test", nil)
	if w.Code != 200 {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		OK        bool     `json:"ok"`
		ToolCount int      `json:"tool_count"`
		Tools     []string `json:"tools"`
		LatencyMs int64    `json:"latency_ms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode test resp: %v body=%s", err, w.Body.String())
	}
	if !resp.OK {
		t.Fatalf("test should be ok, body=%s", w.Body.String())
	}
	if resp.ToolCount != 2 {
		t.Fatalf("want 2 tools, got %d", resp.ToolCount)
	}
	if !contains(resp.Tools, "tool_a") || !contains(resp.Tools, "tool_b") {
		t.Fatalf("missing tools: %v", resp.Tools)
	}
	if atomic.LoadInt32(&initCount) != 1 || atomic.LoadInt32(&listCount) != 1 {
		t.Fatalf("mock server should be hit exactly once each, got init=%d list=%d", initCount, listCount)
	}
}

func TestMCPHandler_TestEndpoint_ReportsFailure(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))

	// Point at a closed port: connection refused.
	w := doJSON(r, "POST", "/mcp-servers", createMCPReq{Name: "dead", URL: "http://127.0.0.1:1/mcp", Transport: "http"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created store.MCPServer
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Tight client timeout so the test stays fast.
	w = doJSONWithTimeout(r, "POST", "/mcp-servers/"+created.ID+"/test", nil, 10*time.Second)
	if w.Code != 200 {
		t.Fatalf("test: want 200 (ok=false payload), got %d", w.Code)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.OK {
		t.Fatalf("test should report ok=false, body=%s", w.Body.String())
	}
	if resp.Error == "" {
		t.Fatalf("test should include an error message, body=%s", w.Body.String())
	}
}

func TestMCPHandler_TestEndpoint_CustomAuthAndSession(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()

	const sessionID = "quake-session"
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-QuakeToken"); got != "quake-token" {
			http.Error(w, "missing custom auth", http.StatusUnauthorized)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		method, _ := req["method"].(string)
		id, _ := req["id"].(float64)
		switch method {
		case "initialize":
			if got := r.Header.Get("Mcp-Session-Id"); got != "" {
				http.Error(w, "unexpected initial session", http.StatusBadRequest)
				return
			}
			w.Header().Set("Mcp-Session-Id", sessionID)
			writeJSONRPC(w, int(id), map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "session-mock", "version": "1.0.0"},
			})
		case "tools/list":
			if got := r.Header.Get("Mcp-Session-Id"); got != sessionID {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			writeJSONRPC(w, int(id), map[string]any{
				"tools": []map[string]any{{"name": "quake_service_data"}},
			})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
	defer mock.Close()

	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	w := doJSON(r, "POST", "/mcp-servers", createMCPReq{
		Name: "quake", URL: mock.URL, Transport: "http", Token: "quake-token",
		AuthHeader: strPtr("X-QuakeToken"), AuthScheme: strPtr(""),
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created store.MCPServer
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.AuthHeader != "X-QuakeToken" || created.AuthScheme != "" {
		t.Fatalf("custom auth not persisted: header=%q scheme=%q", created.AuthHeader, created.AuthScheme)
	}

	w = doJSON(r, "POST", "/mcp-servers/"+created.ID+"/test", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "quake_service_data") {
		t.Fatalf("session-aware probe failed: %d %s", w.Code, w.Body.String())
	}
}

func TestMCPHandler_ListEnabled_OnlyReturnsEnabled(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	ctx := context.Background()
	for _, name := range []string{"a", "b", "c"} {
		enabled := name != "b"
		m := &store.MCPServer{Name: name, URL: "http://x/" + name, Transport: "http", Enabled: enabled}
		if err := stores.MCPServers.Create(ctx, m); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	got, err := stores.MCPServers.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 enabled, got %d (%v)", len(got), namesOf(got))
	}
	for _, m := range got {
		if m.Name == "b" {
			t.Fatalf("disabled server leaked: %v", m)
		}
	}
}

// fakeAgent is a test AgentClient that returns canned numbers without
// touching the network. The real HTTP path is exercised by hand in the
// manual /v1 integration — the unit test just needs to confirm the
// handler's response shape and error propagation.
type fakeAgent struct {
	connected int
	total     int
	err       error
	status    map[string]any
	statusErr error
}

func (f *fakeAgent) ReloadMCPs(_ context.Context) (int, int, error) {
	return f.connected, f.total, f.err
}

func (f *fakeAgent) MCPStatus(_ context.Context) (map[string]any, error) {
	return f.status, f.statusErr
}

func TestMCPHandler_Active_ExposesTokens(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	// Three servers: two enabled (with tokens), one disabled.
	enabled := []*store.MCPServer{
		{Name: "nuclei", URL: "http://n/mcp", Transport: "http", Token: "tok-nuclei", Enabled: true, Description: "nuclei scanner"},
		{Name: "burp", URL: "http://b/mcp", Transport: "http", Token: "tok-burp", Enabled: true},
	}
	for _, m := range enabled {
		if err := stores.MCPServers.Create(ctx, m); err != nil {
			t.Fatalf("seed enabled: %v", err)
		}
	}
	disabled := &store.MCPServer{Name: "waf", URL: "http://w/mcp", Transport: "http", Token: "tok-waf", Enabled: false}
	if err := stores.MCPServers.Create(ctx, disabled); err != nil {
		t.Fatalf("seed disabled: %v", err)
	}

	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	w := doJSON(r, "GET", "/mcp-servers/active", nil)
	if w.Code != 200 {
		t.Fatalf("active: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Servers) != 2 {
		t.Fatalf("want 2 enabled servers, got %d (%v)", len(resp.Servers), resp.Servers)
	}
	tokensByName := map[string]string{}
	for _, s := range resp.Servers {
		name, _ := s["name"].(string)
		token, _ := s["token"].(string)
		tokensByName[name] = token
	}
	if tokensByName["nuclei"] != "tok-nuclei" || tokensByName["burp"] != "tok-burp" {
		t.Fatalf("tokens not exposed to agent: %v", tokensByName)
	}
	// The disabled row must NOT be present.
	if _, ok := tokensByName["waf"]; ok {
		t.Fatalf("disabled server leaked into /active: %v", tokensByName)
	}
}

func TestMCPHandler_Reload_ReportsAgentCounts(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, &fakeAgent{connected: 3, total: 5})
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	w := doJSON(r, "POST", "/mcp-servers/reload", nil)
	if w.Code != 200 {
		t.Fatalf("reload: want 200, got %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Connected int `json:"connected"`
		Reloaded  int `json:"reloaded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Connected != 3 || resp.Reloaded != 5 {
		t.Fatalf("counts wrong: %+v", resp)
	}
}

func TestMCPHandler_Reload_ReturnsBadGatewayOnAgentError(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, &fakeAgent{err: fmt.Errorf("connection refused")})
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	w := doJSON(r, "POST", "/mcp-servers/reload", nil)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("reload: want 502, got %d", w.Code)
	}
}

func TestMCPHandler_Reload_Returns503WhenNoAgent(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	w := doJSON(r, "POST", "/mcp-servers/reload", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("reload: want 503, got %d", w.Code)
	}
}

func TestMCPHandler_AgentStatus_PassesThrough(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	payload := map[string]any{
		"last_reload_at":    1700000000.0,
		"last_reload_error": "",
		"servers": []any{
			map[string]any{"name": "nuclei", "status": "connected", "tool_count": 3, "tools": []any{"nuclei_scan"}, "error": ""},
		},
	}
	h := NewMCPHandler(stores, &fakeAgent{status: payload})
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	w := doJSON(r, "GET", "/mcp-servers/agent-status", nil)
	if w.Code != 200 {
		t.Fatalf("agent-status: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["last_reload_at"] == nil {
		t.Fatalf("missing last_reload_at in %v", resp)
	}
	servers, _ := resp["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
}

func TestMCPHandler_AgentStatus_502OnAgentError(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, &fakeAgent{statusErr: fmt.Errorf("connection refused")})
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))
	w := doJSON(r, "GET", "/mcp-servers/agent-status", nil)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("agent-status: want 502, got %d", w.Code)
	}
}

// --- helpers -----------------------------------------------------------

// Register mounts the handler at the given group root. Keeps the test
// independent of the real router wiring. The `/active` route is declared
// BEFORE `/mcp-servers/:id` because gin resolves static paths first only
// when registered first; declaring it after would treat "active" as :id.
func (h *MCPHandler) Register(g *gin.RouterGroup) {
	g.POST("/mcp-servers", h.Create)
	g.GET("/mcp-servers", h.List)
	g.GET("/mcp-servers/active", h.Active)
	g.GET("/mcp-servers/agent-status", h.AgentStatus)
	g.GET("/mcp-servers/:id", h.Get)
	g.PUT("/mcp-servers/:id", h.Update)
	g.DELETE("/mcp-servers/:id", h.Delete)
	g.POST("/mcp-servers/:id/test", h.Test)
	g.POST("/mcp-servers/reload", h.Reload)
}

// bypassAuth is a no-op middleware so tests can focus on the handler
// without dragging in the admin-token plumbing.
func bypassAuth() gin.HandlerFunc { return func(c *gin.Context) { c.Next() } }

func doJSON(r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	return doJSONWithTimeout(r, method, path, body, 30*time.Second)
}

func doJSONWithTimeout(r http.Handler, method, path string, body any, t time.Duration) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv := &http.Server{Handler: r, ReadHeaderTimeout: t}
	defer srv.Close()
	srv.Handler.ServeHTTP(w, req)
	return w
}

func writeJSONRPC(w http.ResponseWriter, id int, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func namesOf(ms []*store.MCPServer) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// guard against a future helper being unused
var _ = strings.TrimSpace

func TestClassifyURLPrivate(t *testing.T) {
	cases := []struct {
		url  string
		priv bool
	}{
		{"http://localhost:9124/message", true},
		{"http://127.0.0.1:9999/mcp", true},
		{"http://10.0.0.5/mcp", true},
		{"http://192.168.1.1/mcp", true},
		{"http://169.254.169.254/latest/meta-data", true},
		{"http://metadata.google.internal/", true},
		{"http://[::1]:8080/mcp", true},
		{"http://example.com/mcp", false},
		{"https://mcp.example.net/", false},
		{"ftp://x", false}, // not http(s), classify bails
	}
	for _, c := range cases {
		got, note := classifyURLPrivate(c.url)
		if got != c.priv {
			t.Errorf("classifyURLPrivate(%q) = %v (note %q), want %v", c.url, got, note, c.priv)
		}
	}
}

func TestMCPHandler_Create_FlagsPrivateURL(t *testing.T) {
	stores, cleanup := newMCPTestEnv(t)
	defer cleanup()
	h := NewMCPHandler(stores, nil)
	r := gin.New()
	r.Use(bypassAuth())
	h.Register(r.Group(""))

	w := doJSON(r, "POST", "/mcp-servers", createMCPReq{Name: "meta", URL: "http://169.254.169.254/mcp", Transport: "http"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if raw["private"] != true {
		t.Fatalf("private flag not set on metadata URL: %v", raw)
	}
	if raw["private_note"] == "" {
		t.Fatalf("private_note missing: %v", raw)
	}

	// A public URL is not flagged (fresh map — Unmarshal into an existing
	// map keeps leftover keys from the previous decode).
	var rawPub map[string]any
	w = doJSON(r, "POST", "/mcp-servers", createMCPReq{Name: "pub", URL: "https://mcp.example.com", Transport: "http"})
	_ = json.Unmarshal(w.Body.Bytes(), &rawPub)
	if rawPub["private"] == true || rawPub["private_note"] != nil {
		t.Fatalf("public URL wrongly flagged: %v", rawPub)
	}
}
