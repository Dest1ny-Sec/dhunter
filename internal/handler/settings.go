// SettingsHandler exposes platform configuration: LLM provider config
// (import + test, ccswitch-style), and token-budget red lines.
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

const (
	KeyLLMConfig = "llm_config"
	KeyBudget    = "token_budget"
)

// LLMConfig is the persisted LLM provider configuration.
type LLMConfig struct {
	Provider  string `json:"provider"`   // anthropic | openai
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key,omitempty"`
	MaxTokens int    `json:"max_tokens"`
}

// SettingsHandler holds stores for config persistence.
type SettingsHandler struct {
	Stores *store.Stores
}

// NewSettingsHandler constructs a SettingsHandler.
func NewSettingsHandler(s *store.Stores) *SettingsHandler {
	return &SettingsHandler{Stores: s}
}

// GetLLM returns the saved LLM config. The endpoint is behind the admin
// token, so it returns the REAL key — the agent uses this to configure its
// runs, and masking here would break it with a 401.
func (h *SettingsHandler) GetLLM(c *gin.Context) {
	cfg := h.loadLLM(c.Request.Context())
	c.JSON(http.StatusOK, cfg)
}

type llmSaveReq struct {
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
	MaxTokens int    `json:"max_tokens"`
}

// SaveLLM persists the LLM config.
func (h *SettingsHandler) SaveLLM(c *gin.Context) {
	var req llmSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if strings.TrimSpace(req.Model) == "" || strings.TrimSpace(req.BaseURL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model and base_url required"})
		return
	}
	// If no new key is provided, keep the existing one.
	if strings.TrimSpace(req.APIKey) == "" {
		existing := h.loadLLM(c.Request.Context())
		req.APIKey = existing.APIKey
	}
	if req.Provider == "" {
		req.Provider = detectProviderFromURL(req.BaseURL)
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 8192
	}
	cfg := LLMConfig{Provider: req.Provider, BaseURL: req.BaseURL, Model: req.Model, APIKey: req.APIKey, MaxTokens: req.MaxTokens}
	data, _ := json.Marshal(cfg)
	if err := h.Stores.Settings.Set(c.Request.Context(), KeyLLMConfig, string(data)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "config": maskConfig(cfg)})
}

// TestLLM does a tiny real call against the given (or saved) config and
// reports whether the model is reachable — the ccswitch-style "does it work?"
// check.
func (h *SettingsHandler) TestLLM(c *gin.Context) {
	var req llmSaveReq
	_ = c.ShouldBindJSON(&req) // optional; fall back to saved config

	cfg := h.loadLLM(c.Request.Context())
	if req.Model != "" && req.BaseURL != "" {
		cfg = LLMConfig{Provider: req.Provider, BaseURL: req.BaseURL, Model: req.Model, APIKey: req.APIKey, MaxTokens: 8}
		if cfg.Provider == "" {
			cfg.Provider = detectProviderFromURL(cfg.BaseURL)
		}
		if cfg.APIKey == "" {
			// test with the saved key if the user only changed model/url
			cfg.APIKey = h.loadLLM(c.Request.Context()).APIKey
		}
	}
	if cfg.APIKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "API key is required (add it or set env DHUNTER_LLM_KEY)"})
		return
	}

	start := time.Now()
	ok, msg := testModelReachable(c.Request.Context(), cfg)
	c.JSON(http.StatusOK, gin.H{
		"ok":         ok,
		"model":      cfg.Model,
		"provider":   cfg.Provider,
		"latency_ms": time.Since(start).Milliseconds(),
		"detail":     msg,
	})
}

// GetBudget returns the per-run token budget red line.
func (h *SettingsHandler) GetBudget(c *gin.Context) {
	raw, _ := h.Stores.Settings.Get(c.Request.Context(), KeyBudget)
	v := 0
	fmt.Sscanf(raw, "%d", &v)
	if v <= 0 {
		v = 0 // 0 = unlimited
	}
	c.JSON(http.StatusOK, gin.H{"max_run_tokens": v})
}

// SaveBudget persists the per-run token budget.
func (h *SettingsHandler) SaveBudget(c *gin.Context) {
	var body struct {
		MaxRunTokens int `json:"max_run_tokens"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if err := h.Stores.Settings.Set(c.Request.Context(), KeyBudget, fmt.Sprintf("%d", body.MaxRunTokens)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "max_run_tokens": body.MaxRunTokens})
}

// --- helpers -------------------------------------------------------------

func (h *SettingsHandler) loadLLM(ctx context.Context) LLMConfig {
	raw, _ := h.Stores.Settings.Get(ctx, KeyLLMConfig)
	if raw == "" {
		return LLMConfig{}
	}
	var cfg LLMConfig
	_ = json.Unmarshal([]byte(raw), &cfg)
	return cfg
}

func maskConfig(cfg LLMConfig) LLMConfig {
	cfg.APIKey = maskKey(cfg.APIKey)
	return cfg
}

func maskKey(k string) string {
	if len(k) <= 6 {
		return "****"
	}
	return k[:3] + "****" + k[len(k)-3:]
}

func detectProviderFromURL(baseURL string) string {
	u := strings.ToLower(baseURL)
	if strings.Contains(u, "/anthropic") {
		return "anthropic"
	}
	return "openai"
}

// testModelReachable sends a 1-token "ping" to the provider.
func testModelReachable(ctx context.Context, cfg LLMConfig) (bool, string) {
	hc := &http.Client{Timeout: 15 * time.Second}
	base := strings.TrimRight(cfg.BaseURL, "/")
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if cfg.Provider == "anthropic" {
		url := base + "/v1/messages"
		headers["x-api-key"] = cfg.APIKey
		headers["authorization"] = "Bearer " + cfg.APIKey
		headers["anthropic-version"] = "2023-06-01"
		body, _ := json.Marshal(map[string]interface{}{
			"model": cfg.Model, "max_tokens": 8,
			"messages": []map[string]string{{"role": "user", "content": "ping"}},
		})
		req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if resp.StatusCode >= 400 {
			return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(b, 200))
		}
		return true, "connected"
	}
	// openai-compatible
	url := base + "/chat/completions"
	headers["Authorization"] = "Bearer " + cfg.APIKey
	body, _ := json.Marshal(map[string]interface{}{
		"model": cfg.Model, "max_tokens": 8,
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 400 {
		return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(b, 200))
	}
	return true, "connected"
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n]
	}
	return s
}
