package toolbelt

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// toolDef is one MCP tool definition (Anthropic-style schema).
type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

func strMap() map[string]interface{} {
	return map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "string"}}
}

// toolsList returns every tool the agent can call.
func toolsList() []toolDef {
	str := func(t, d string) map[string]string { return map[string]string{"type": t, "description": d} }
	return []toolDef{
		{Name: "fofa_search", Description: "FOFA asset search. Needs FOFA_EMAIL/FOFA_KEY (env or args). query is FOFA syntax.",
			InputSchema: obj(map[string]interface{}{
				"query": str("string", "FOFA query, e.g. domain=\"example.com\" or title=\"login\""),
				"size":  str("integer", "max results, default 100, cap 10000"),
				"email": str("string", ""), "key": str("string", ""),
			}, []string{"query"})},
		{Name: "subfinder_enum", Description: "Subdomain enumeration via the subfinder binary.",
			InputSchema: obj(map[string]interface{}{
				"domain": str("string", ""), "threads": str("integer", "optional"), "bin": str("string", "binary path"),
			}, []string{"domain"})},
		{Name: "assetfinder_enum", Description: "Passive subdomain enumeration via assetfinder.",
			InputSchema: obj(map[string]interface{}{"domain": str("string", "")}, []string{"domain"})},
		{Name: "baidu_search", Description: "Baidu search (may hit anti-bot).",
			InputSchema: obj(map[string]interface{}{"query": str("string", ""), "num": str("integer", "default 10")}, []string{"query"})},
		{Name: "bing_search", Description: "Bing search (CN, usually reachable).",
			InputSchema: obj(map[string]interface{}{"query": str("string", ""), "num": str("integer", "default 10")}, []string{"query"})},
		{Name: "icp_lookup", Description: "ICP record lookup (aizhan for domains, Baidu for company names).",
			InputSchema: obj(map[string]interface{}{"keyword": str("string", "domain (exact) or company name (broad)")}, []string{"keyword"})},
		{Name: "fetch_js", Description: "Fetch a page's JS assets and return their source (katana, curl fallback).",
			InputSchema: obj(map[string]interface{}{
				"url": str("string", ""), "depth": str("integer", "default 2"), "max": str("integer", "default 30"),
			}, []string{"url"})},
		{Name: "js_analyzer", Description: "Parse JS source and extract URLs, API paths, and credential-like strings.",
			InputSchema: obj(map[string]interface{}{
				"source": str("string", "JS text"), "url": str("string", "origin URL (for context)"),
			}, []string{"source"})},
		{Name: "katana_crawl", Description: "Crawl a site for URLs via katana.",
			InputSchema: obj(map[string]interface{}{"url": str("string", ""), "depth": str("integer", "default 2")}, []string{"url"})},
		{Name: "leak_creds", Description: "Probe ~50 common sensitive paths (.git/.env/actuator/swagger etc.).",
			InputSchema: obj(map[string]interface{}{"url": str("string", "")}, []string{"url"})},
		{Name: "gau_history", Description: "Historical URLs via gau.",
			InputSchema: obj(map[string]interface{}{"domain": str("string", "")}, []string{"domain"})},
		{Name: "wayback_history", Description: "Wayback Machine URLs via waybackurls.",
			InputSchema: obj(map[string]interface{}{"domain": str("string", "")}, []string{"domain"})},
		{Name: "httpx_probe", Description: "Probe status/title/tech for a list of hosts via httpx.",
			InputSchema: obj(map[string]interface{}{
				"target":  str("string", ""), "targets": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
			}, nil)},
		{Name: "waf_detect", Description: "Heuristic WAF detection (headers + behavior + block-page text).",
			InputSchema: obj(map[string]interface{}{"url": str("string", "")}, []string{"url"})},
		{Name: "http_request", Description: "Send an arbitrary HTTP request (method/headers/cookies/body). The agent's probing workhorse.",
			InputSchema: obj(map[string]interface{}{
				"method": str("string", "GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS"), "url": str("string", "absolute URL"),
				"headers": strMap(), "cookies": strMap(), "body": str("string", "raw body"),
				"timeout":  str("integer", "seconds, default 15"),
				"insecure": str("boolean", "skip TLS verify, default false (verify)"),
			}, []string{"url"})},
		{Name: "api_fuzz", Description: "Mutate one parameter across a wordlist (query|body|header) and report response deltas.",
			InputSchema: obj(map[string]interface{}{
				"url": str("string", ""), "param_name": str("string", "parameter to fuzz"),
				"param_loc": str("string", "query|body|header"),
				"wordlist":  map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
				"concurrency": str("integer", "default 5, cap 20"),
			}, []string{"url", "wordlist"})},
		{Name: "auth_bypass_check", Description: "Try ~20 auth-bypass tricks (path/method/IP headers/Host) against a URL.",
			InputSchema: obj(map[string]interface{}{
				"url": str("string", ""), "headers": strMap(), "cookies": strMap(),
			}, []string{"url"})},
		{Name: "poc_scaffold", Description: "Generate a starting PoC (curl) for a vuln type; the agent fills in payloads.",
			InputSchema: obj(map[string]interface{}{
				"vuln_type": str("string", "sqli|xss|ssrf|rce|lfi|auth-bypass|idor|info-leak"),
				"url":       str("string", ""), "method": str("string", "default GET"), "body": str("string", ""),
			}, []string{"vuln_type", "url"})},
		{Name: "risk_score", Description: "Heuristic 0-100 risk score from evidence+impact (agent should override).",
			InputSchema: obj(map[string]interface{}{
				"impact": str("string", "critical|high|medium|low"), "evidence": str("string", ""),
			}, []string{"impact", "evidence"})},
		{Name: "write_finding", Description: "Record a CONFIRMED vulnerability to the platform. Call ONLY with reproducible evidence. Requires run_id.",
			InputSchema: obj(map[string]interface{}{
				"run_id": str("string", "the current run's id (the agent passes it automatically)"),
				"title": str("string", ""), "severity": str("string", "critical|high|medium|low|info"),
				"target": str("string", "affected URL/host"), "evidence": str("string", "PoC + proof"),
			}, []string{"title", "run_id"})},
	}
}

func obj(props map[string]interface{}, required []string) map[string]interface{} {
	m := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// callTool dispatches a tool by name. It tolerates a `__`-prefixed name
// (some platforms add a server prefix like `dhunter__http_request`).
func callTool(ctx context.Context, name string, args map[string]interface{}) toolResult {
	if idx := strings.Index(name, "__"); idx > 0 {
		name = name[idx+2:]
	}
	switch name {
	case "fofa_search":
		return fofaSearch(ctx, args)
	case "subfinder_enum":
		return subfinderEnum(ctx, args)
	case "assetfinder_enum":
		return assetfinderEnum(ctx, args)
	case "baidu_search":
		return baiduSearch(ctx, args)
	case "bing_search":
		return bingSearch(ctx, args)
	case "icp_lookup":
		return icpLookup(ctx, args)
	case "fetch_js":
		return fetchJS(ctx, args)
	case "js_analyzer":
		return jsAnalyzer(ctx, args)
	case "katana_crawl":
		return katanaCrawl(ctx, args)
	case "leak_creds":
		return leakCreds(ctx, args)
	case "gau_history":
		return gauHistory(ctx, args)
	case "wayback_history":
		return waybackHistory(ctx, args)
	case "httpx_probe":
		return httpxProbe(ctx, args)
	case "waf_detect":
		return wafDetect(ctx, args)
	case "http_request":
		return httpRequest(ctx, args)
	case "api_fuzz":
		return apiFuzz(ctx, args)
	case "auth_bypass_check":
		return authBypass(ctx, args)
	case "poc_scaffold":
		return pocScaffold(ctx, args)
	case "risk_score":
		return riskScore(ctx, args)
	case "write_finding":
		return writeFinding(ctx, args)
	}
	return errResult("unknown tool: " + name)
}

// --- JSON-RPC wire protocol -------------------------------------------

type rpcRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id,omitempty"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// HandleJSONRPC serves the MCP streamable-HTTP endpoint at /message.
func HandleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: nil,
			Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	ctx := r.Context()
	var result interface{}
	var rpcErr *rpcError
	switch req.Method {
	case "initialize":
		result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": false}},
			"serverInfo":      map[string]interface{}{"name": "dhunter-mcp", "version": "1.0.0"},
		}
	case "notifications/initialized":
		w.WriteHeader(http.StatusNoContent)
		return
	case "tools/list":
		result = map[string]interface{}{"tools": toolsList()}
	case "tools/call":
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]interface{})
		if args == nil {
			args = map[string]interface{}{}
		}
		result = callTool(ctx, name, args)
	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Auth wraps the mux in a bearer-token gate (healthz is public).
func Auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")) != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serve starts the MCP HTTP server (blocking).
func Serve(addr, token string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]interface{}{
			"status": "ok", "service": "dhunter-mcp", "tools": len(toolsList()),
			"ts": time.Now().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/message", HandleJSONRPC)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			writeJSON(w, 200, map[string]interface{}{"service": "dhunter-mcp", "version": "1.0.0", "tools": len(toolsList())})
			return
		}
		http.NotFound(w, r)
	})
	log.Printf("dhunter-mcp: listening on %s (%d tools)", addr, len(toolsList()))
	return http.ListenAndServe(addr, Auth(token, mux))
}
