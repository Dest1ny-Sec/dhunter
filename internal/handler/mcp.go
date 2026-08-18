package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

// AgentClient is the minimum interface the MCP handler needs to ask the
// Python agent to reload its external MCP connections. The router wires
// the real client; tests can pass a stub.
type AgentClient interface {
	ReloadMCPs(ctx context.Context) (int, int, error) // connected, total, err
	MCPStatus(ctx context.Context) (map[string]any, error)
}

// HTTPAgentClient is the production AgentClient: it POSTs to the
// agent's /v1/mcp/reload over the admin token.
type HTTPAgentClient struct {
	BaseURL string
	Token   string
	Timeout time.Duration
}

func (a *HTTPAgentClient) ReloadMCPs(ctx context.Context) (int, int, error) {
	if a == nil || a.BaseURL == "" {
		return 0, 0, fmt.Errorf("agent not configured")
	}
	url := strings.TrimRight(a.BaseURL, "/") + "/v1/mcp/reload"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, 0, fmt.Errorf("agent reload HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Connected int `json:"connected"`
		Reloaded  int `json:"reloaded"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, 0, fmt.Errorf("agent reload: decode: %w", err)
	}
	return out.Connected, out.Reloaded, nil
}

// MCPStatus proxies GET /v1/mcp/status — the agent's snapshot of its
// external MCP connections (per-server ready/error, last_reload_at).
// The Settings UI uses this to render the "上次同步" indicator and
// per-row green/gray dots.
func (a *HTTPAgentClient) MCPStatus(ctx context.Context) (map[string]any, error) {
	if a == nil || a.BaseURL == "" {
		return nil, fmt.Errorf("agent not configured")
	}
	url := strings.TrimRight(a.BaseURL, "/") + "/v1/mcp/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("agent status HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out map[string]any
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("agent status: decode: %w", err)
	}
	return out, nil
}

// MCPHandler handles CRUD + connection-test for user-configured external
// MCP servers (the "extension center"). Built-in dhunter-mcp is unaffected
// and is always-on via env vars — the rows here are pure add-ons.
type MCPHandler struct {
	Stores *store.Stores
	Agent  AgentClient
}

func NewMCPHandler(s *store.Stores, agent AgentClient) *MCPHandler {
	return &MCPHandler{Stores: s, Agent: agent}
}

// safeNameRE allows ASCII letters/digits/underscore/hyphen/dot; we use
// it to namespace external tool names (<name>::<tool>) so they don't
// collide with the built-in toolbelt.
var safeNameRE = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)

// createMCPReq is the JSON body for POST /api/mcp-servers.
type createMCPReq struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Transport   string `json:"transport"`
	Token       string `json:"token"`
	Enabled     *bool  `json:"enabled"`
	Description string `json:"description"`
}

// updateMCPReq is the JSON body for PUT /api/mcp-servers/:id.
type updateMCPReq struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Transport   string `json:"transport"`
	Token       string `json:"token"`       // empty = keep; non-empty = replace
	ClearToken  bool   `json:"clear_token"` // explicit wipe (frontend sends this)
	Enabled     *bool  `json:"enabled"`
	Description string `json:"description"`
}

// List handles GET /api/mcp-servers — token is redacted; HasToken
// indicates whether one is stored.
func (h *MCPHandler) List(c *gin.Context) {
	rows, err := h.Stores.MCPServers.List(c.Request.Context(), 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, m := range rows {
		annotatePrivate(m)
	}
	c.JSON(http.StatusOK, gin.H{"servers": rows})
}

// Get handles GET /api/mcp-servers/:id.
func (h *MCPHandler) Get(c *gin.Context) {
	id := c.Param("id")
	m, err := h.Stores.MCPServers.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "mcp server not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	annotatePrivate(m)
	c.JSON(http.StatusOK, m)
}

// Create handles POST /api/mcp-servers. The token is returned in the
// response exactly once (so the user can save it client-side); later
// reads redacted.
func (h *MCPHandler) Create(c *gin.Context) {
	var body createMCPReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.URL = strings.TrimSpace(body.URL)
	body.Transport = strings.ToLower(strings.TrimSpace(body.Transport))
	if !safeNameRE.MatchString(body.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must match ^[A-Za-z0-9_.-]{1,64}$ (used to namespace tools)"})
		return
	}
	if !isHTTPUrl(body.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url must be an http(s):// URL"})
		return
	}
	if body.Transport == "" {
		body.Transport = "http"
	}
	if body.Transport != "http" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transport must be \"http\" in v0.7.0"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	m := &store.MCPServer{
		Name:        body.Name,
		URL:         body.URL,
		Transport:   body.Transport,
		Token:       body.Token,
		Enabled:     enabled,
		Description: strings.TrimSpace(body.Description),
		CreatedAt:   time.Now().UTC(),
	}
	if err := h.Stores.MCPServers.Create(c.Request.Context(), m); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Reflect the just-stored token in HasToken so the response
	// truthfully reports whether a credential is on file.
	m.HasToken = m.Token != ""
	annotatePrivate(m)
	// Token is returned exactly once so the caller can save it. All
	// later reads (List/Get/Update) redact it via the MCPServer custom
	// MarshalJSON method.
	c.JSON(http.StatusCreated, m.WithToken())
}

// Update handles PUT /api/mcp-servers/:id. Token is preserved unless
// the request body sets a new one or `clear_token: true`.
func (h *MCPHandler) Update(c *gin.Context) {
	id := c.Param("id")
	existing, err := h.Stores.MCPServers.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "mcp server not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var body updateMCPReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.URL = strings.TrimSpace(body.URL)
	body.Transport = strings.ToLower(strings.TrimSpace(body.Transport))
	if body.Name == "" {
		body.Name = existing.Name
	}
	if !safeNameRE.MatchString(body.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must match ^[A-Za-z0-9_.-]{1,64}$"})
		return
	}
	if body.URL == "" {
		body.URL = existing.URL
	}
	if !isHTTPUrl(body.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url must be an http(s):// URL"})
		return
	}
	if body.Transport == "" {
		body.Transport = existing.Transport
	}
	if body.Transport != "http" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "transport must be \"http\" in v0.7.0"})
		return
	}
	enabled := existing.Enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	updated := *existing
	updated.Name = body.Name
	updated.URL = body.URL
	updated.Transport = body.Transport
	updated.Enabled = enabled
	updated.Description = strings.TrimSpace(body.Description)
	if err := h.Stores.MCPServers.Update(c.Request.Context(), &updated); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "mcp server not found"})
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Token is a separate column to avoid accidentally wiping it when
	// the caller updates other fields without re-supplying the token.
	switch {
	case body.ClearToken:
		_ = h.Stores.MCPServers.SetToken(c.Request.Context(), id, "")
	case body.Token != "":
		_ = h.Stores.MCPServers.SetToken(c.Request.Context(), id, body.Token)
	}
	final, err := h.Stores.MCPServers.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	annotatePrivate(final)
	c.JSON(http.StatusOK, final)
}

// Delete handles DELETE /api/mcp-servers/:id. Idempotent.
func (h *MCPHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.Stores.MCPServers.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// Test handles POST /api/mcp-servers/:id/test — speaks `initialize` +
// `tools/list` against the configured URL and returns the tool names.
// This is the user's "did I wire it up right" smoke test.
func (h *MCPHandler) Test(c *gin.Context) {
	id := c.Param("id")
	m, err := h.Stores.MCPServers.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "mcp server not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tools, latencyMs, err := probeMCP(c.Request.Context(), m.URL, m.Token, 8*time.Second)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":         false,
			"server":     m.Name,
			"url":        m.URL,
			"error":      err.Error(),
			"latency_ms": latencyMs,
		})
		return
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if name, _ := t["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"server":     m.Name,
		"url":        m.URL,
		"tool_count": len(names),
		"tools":      names,
		"latency_ms": latencyMs,
	})
}

// Reload asks the Python agent to re-fetch /api/mcp-servers/active and
// reconnect. Lets the user add a new server in the Settings UI and pick
// it up without restarting the agent. Returns the agent's count of
// successfully connected external servers so the UI can confirm.
func (h *MCPHandler) Reload(c *gin.Context) {
	if h.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent bridge not configured"})
		return
	}
	connected, total, err := h.Agent.ReloadMCPs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"reloaded": total, "connected": connected})
}

// AgentStatus handles GET /api/mcp-servers/agent-status — proxies the
// agent's external MCP snapshot (per-server ready/error and
// last_reload_at). The UI uses this for the "上次同步" indicator and
// per-row green/gray dots. Returns 502 when the agent is unreachable;
// the frontend treats that as "agent offline — last sync unknown".
func (h *MCPHandler) AgentStatus(c *gin.Context) {
	if h.Agent == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent bridge not configured"})
		return
	}
	st, err := h.Agent.MCPStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

// Active handles GET /api/mcp-servers/active — returns only the enabled
// servers AND exposes the (otherwise redacted) token. This endpoint is
// exclusively for the Python agent, which needs the token to auth when
// it bootstraps. Same Bearer auth as the rest of /api, so the agent
// already has access via DHUNTER_BACKEND_TOKEN.
func (h *MCPHandler) Active(c *gin.Context) {
	rows, err := h.Stores.MCPServers.ListEnabled(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Return a flat, agent-friendly shape: keep the token in the
	// response (this endpoint is the "leak the credential to the
	// in-cluster caller" door).
	out := make([]gin.H, 0, len(rows))
	for _, m := range rows {
		out = append(out, gin.H{
			"id":          m.ID,
			"name":        m.Name,
			"url":         m.URL,
			"transport":   m.Transport,
			"token":       m.Token,
			"description": m.Description,
		})
	}
	c.JSON(http.StatusOK, gin.H{"servers": out})
}

// probeMCP does the minimum JSON-RPC handshake (initialize + tools/list)
// against an MCP streamable-HTTP endpoint and returns the declared tools.
// It is shared by Test (UI smoke test) and could be reused by the agent
// bootstrap path if we ever probe instead of trusting config.
func probeMCP(ctx context.Context, url, token string, timeout time.Duration) ([]map[string]any, int64, error) {
	start := time.Now()
	if !isHTTPUrl(url) {
		return nil, time.Since(start).Milliseconds(), fmt.Errorf("invalid url")
	}
	// 1. initialize — protocolVersion is the same constant we ship in the
	//    Python agent so a compatible server replies with a matching cap.
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "dhunter-test", "version": "0.7.0"},
		},
	}
	if _, err := rpcOnce(ctx, url, token, initReq, timeout); err != nil {
		return nil, time.Since(start).Milliseconds(), fmt.Errorf("initialize: %w", err)
	}
	// 2. tools/list
	listReq := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}
	res, err := rpcOnce(ctx, url, token, listReq, timeout)
	if err != nil {
		return nil, time.Since(start).Milliseconds(), fmt.Errorf("tools/list: %w", err)
	}
	// rpcOnce already unwraps the JSON-RPC envelope, so `res` IS the
	// result object: {"tools": [...]}.
	rawTools, _ := res["tools"].([]any)
	tools := make([]map[string]any, 0, len(rawTools))
	for _, t := range rawTools {
		if m, ok := t.(map[string]any); ok {
			tools = append(tools, m)
		}
	}
	return tools, time.Since(start).Milliseconds(), nil
}

// rpcOnce fires one JSON-RPC POST and unwraps the response. Mirrors the
// Python MCPClient (POST one request, support JSON or SSE reply).
func rpcOnce(ctx context.Context, url, token string, payload map[string]any, timeout time.Duration) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateForErr(string(raw), 200))
	}
	// SSE: server may reply with an event-stream; pick the first frame
	// with a JSON-RPC id.
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return unwrapSSE(raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("non-JSON response (status %d): %s", resp.StatusCode, truncateForErr(string(raw), 200))
	}
	return unwrapJSONRPC(out)
}

func unwrapJSONRPC(out map[string]any) (map[string]any, error) {
	if e, ok := out["error"]; ok && e != nil {
		return nil, fmt.Errorf("rpc error: %v", e)
	}
	if r, ok := out["result"].(map[string]any); ok {
		return r, nil
	}
	return nil, fmt.Errorf("missing result in response: %v", out)
}

// unwrapSSE finds the first data: frame that is a JSON-RPC reply (has
// `id` and either `result` or `error`) and returns its result map.
// Same algorithm as agents/tools/mcp_client.py.
func unwrapSSE(body []byte) (map[string]any, error) {
	lines := bytes.Split(body, []byte("\n"))
	var buf []string
	for _, line := range lines {
		if len(line) == 0 {
			if len(buf) > 0 {
				joined := strings.Join(buf, "\n")
				buf = buf[:0]
				var msg map[string]any
				if err := json.Unmarshal([]byte(joined), &msg); err == nil {
					if _, hasID := msg["id"]; hasID {
						return unwrapJSONRPC(msg)
					}
				}
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			payload := string(line[len("data:"):])
			if strings.HasPrefix(payload, " ") {
				payload = payload[1:]
			}
			buf = append(buf, payload)
		}
	}
	return nil, fmt.Errorf("SSE response closed without a JSON-RPC reply")
}

func truncateForErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func isHTTPUrl(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// classifyURLPrivate flags addresses the UI should hint about: loopback,
// private ranges, link-local, the cloud metadata service (169.254.169.254),
// and well-known metadata hostnames. We do NOT block these — a user may
// legitimately point at an internal MCP server — but the UI shows a yellow
// warning so an accidental metadata/内网 URL is visible at a glance.
func classifyURLPrivate(raw string) (bool, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false, ""
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.String() == "169.254.169.254" {
			return true, "云元数据服务地址（169.254.169.254）"
		}
		switch {
		case ip.IsLoopback():
			return true, "回环地址（本机）"
		case ip.IsPrivate():
			return true, "私有网段（内网）"
		case ip.IsLinkLocalUnicast():
			return true, "链路本地地址"
		case ip.IsUnspecified():
			return true, "未指定地址"
		}
		return false, ""
	}
	lh := strings.ToLower(host)
	switch {
	case lh == "localhost" || strings.HasSuffix(lh, ".localhost"):
		return true, "localhost（本机）"
	case lh == "metadata.google.internal" || lh == "instance-data" || lh == "instance-data.ec2.internal":
		return true, "云元数据服务主机名"
	}
	return false, ""
}

// annotatePrivate derives and sets the Private/PrivateNote fields on the
// server (derived per-response; nothing is persisted).
func annotatePrivate(m *store.MCPServer) {
	priv, note := classifyURLPrivate(m.URL)
	m.Private = priv
	m.PrivateNote = note
}
