package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/config"
	"github.com/dhunter/dhunter/internal/handler"
	"github.com/dhunter/dhunter/internal/middleware"
	"github.com/dhunter/dhunter/internal/store"
	"github.com/dhunter/dhunter/internal/stream"
)

// buildRouter wires the full HTTP surface (API + SSE + SPA mount). It is
// extracted from main() so contract/E2E tests can boot the REAL router
// against a temp database and a stub agent bridge — the same code path the
// shipped binary serves.
func buildRouter(cfg *config.Config, stores *store.Stores, hub *stream.Hub, bridge handler.RunStarter, adminUser, passwordHash string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())
	router.Use(corsForOrigins(cfg.Server.AllowedOrigins))

	mountWebUI(router)
	router.GET("/api/healthz", handler.Healthz)
	authH := handler.NewAuthHandler(cfg.Admin.Token, adminUser, passwordHash, stores.Settings)
	// Login is the one unauthenticated route that unlocks the admin token —
	// gate it with an in-memory per-IP rate limiter so an exposed port can't
	// be password-sprayed without tripping 429s.
	loginLimiter := middleware.NewLoginLimiter(10, time.Minute)
	router.POST("/api/auth/login", loginLimiter.Middleware(), authH.Login)

	// Authenticated API group.
	api := router.Group("/api")
	api.Use(middleware.Bearer(cfg.Admin.Token))
	{
		api.POST("/auth/change", authH.Change)
		targetH := handler.NewTargetHandler(stores)
		api.POST("/targets", targetH.Create)
		api.GET("/targets", targetH.List)
		api.GET("/targets/:id", targetH.Get)
		api.PATCH("/targets/:id/auth", targetH.SetAuth)
		api.PATCH("/targets/:id/redlines", targetH.SetRedLines)
		api.PATCH("/targets/:id/favorite", targetH.SetFavorite)
		api.DELETE("/targets/:id", targetH.Delete)

		runH := handler.NewRunHandler(stores, bridge, hub)
		api.POST("/runs", runH.Create)
		api.POST("/runs/:id/cancel", runH.Cancel)
		api.POST("/runs/:id/pause", runH.Pause)
		api.POST("/runs/:id/continue", runH.Continue)

		runsH := handler.NewRunsHandler(stores)
		api.GET("/runs", runsH.List)
		api.GET("/runs/:id", runsH.Get)
		api.GET("/runs/:id/messages", runsH.Messages)
		api.GET("/runs/:id/vulnerabilities", runsH.Vulnerabilities)
		api.GET("/runs/:id/tool_calls", runsH.ToolCalls)
		api.GET("/targets/:id/runs", runsH.ProjectRuns)

		vulnsH := handler.NewVulnsHandler(stores)
		api.GET("/vulnerabilities", vulnsH.List)
		api.POST("/vulnerabilities", vulnsH.Create)
		api.PATCH("/vulnerabilities/:id", vulnsH.Patch)

		reportH := handler.NewReportHandler(stores)
		api.GET("/runs/:id/report", reportH.Markdown)
		api.GET("/targets/:id/report", reportH.ProjectReport)

		searchH := handler.NewSearchHandler(stores)
		api.GET("/search/messages", searchH.Messages)

		// Lightweight Prometheus-style metrics (runtime + business counters).
		// Auth-gated; scrape-friendly text format, no external deps.
		api.GET("/metrics", func(c *gin.Context) {
			ctx := c.Request.Context()
			runsTotal, _ := stores.Runs.Count(ctx)
			runsActive, _ := stores.Runs.CountActive(ctx)
			vulnsTotal, _ := stores.Vulns.Count(ctx)
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			subscribers := hub.TotalSubscribers()
			var b strings.Builder
			b.WriteString("# HELP dhunter_runs_total Total runs ever started.\n# TYPE dhunter_runs_total gauge\n")
			fmt.Fprintf(&b, "dhunter_runs_total %d\n", runsTotal)
			fmt.Fprintf(&b, "# TYPE dhunter_runs_active gauge\ndhunter_runs_active %d\n", runsActive)
			fmt.Fprintf(&b, "# TYPE dhunter_vulns_total gauge\ndhunter_vulns_total %d\n", vulnsTotal)
			fmt.Fprintf(&b, "# TYPE dhunter_hub_subscribers gauge\ndhunter_hub_subscribers %d\n", subscribers)
			fmt.Fprintf(&b, "# TYPE go_goroutines gauge\ngo_goroutines %d\n", runtime.NumGoroutine())
			fmt.Fprintf(&b, "# TYPE go_memstats_alloc_bytes gauge\ngo_memstats_alloc_bytes %d\n", mem.Alloc)
			c.Header("Content-Type", "text/plain; version=0.0.4")
			c.String(http.StatusOK, b.String())
		})

		// Platform status — lets the UI self-describe the bundled services
		// instead of asking the user to configure them by hand. The server
		// probes its own MCP + agent sidecars (the browser can't, cross-origin).
		// Platform settings — LLM config import/test, token budget.
		settingsH := handler.NewSettingsHandler(stores)
		api.GET("/settings/llm", settingsH.GetLLM)
		api.PUT("/settings/llm", settingsH.SaveLLM)
		api.POST("/settings/llm/test", settingsH.TestLLM)
		api.GET("/settings/budget", settingsH.GetBudget)
		api.PUT("/settings/budget", settingsH.SaveBudget)
		api.POST("/settings/clear-data", settingsH.ClearData)
		api.GET("/knowledge", settingsH.KnowledgeList)
		api.POST("/knowledge", settingsH.KnowledgeAdd)

		// Assets — structured project inventory (subdomains/endpoints/...).
		assetH := handler.NewAssetHandler(stores)
		api.GET("/targets/:id/assets", assetH.List)
		api.POST("/targets/:id/assets", assetH.Create)

		// MCP extension center — user-configured external MCP servers
		// that the agent aggregates into its toolbelt (namespaced as
		// `<server>::<tool>`). The built-in dhunter-mcp is unaffected.
		mcpH := handler.NewMCPHandler(stores, &handler.HTTPAgentClient{
			BaseURL: cfg.Agent.PythonURL,
			Token:   cfg.Agent.Token,
			Timeout: 12 * time.Second,
		})
		api.GET("/mcp-servers", mcpH.List)
		api.GET("/mcp-servers/active", mcpH.Active) // for the agent
		api.GET("/mcp-servers/:id", mcpH.Get)
		api.POST("/mcp-servers", mcpH.Create)
		api.PUT("/mcp-servers/:id", mcpH.Update)
		api.DELETE("/mcp-servers/:id", mcpH.Delete)
		api.POST("/mcp-servers/:id/test", mcpH.Test)
		api.POST("/mcp-servers/reload", mcpH.Reload)

		api.GET("/status", func(c *gin.Context) {			hc := &http.Client{Timeout: 3 * time.Second}
			probe := func(url string) string {
				resp, err := hc.Get(url)
				if err != nil {
					return "disconnected"
				}
				defer resp.Body.Close()
				if resp.StatusCode < 500 {
					return "connected"
				}
				return "disconnected"
			}
			agentURL := cfg.Agent.PythonURL
			mcpBase := strings.TrimSuffix(cfg.MCP.WebHunter.URL, "/message")
			c.JSON(http.StatusOK, gin.H{
				"llm": gin.H{
					"provider": cfg.LLM.Provider,
					"model":    cfg.LLM.Model,
					"base_url": cfg.LLM.BaseURL,
					"key_set":  cfg.LLM.APIKey != "",
				},
				"mcp":   gin.H{"url": cfg.MCP.WebHunter.URL, "status": probe(mcpBase + "/health"), "tools": listMCPTools(cfg.MCP.WebHunter.URL, cfg.MCP.WebHunter.Token), "availability": mcpToolAvailability(mcpBase, cfg.MCP.WebHunter.Token)},
				"agent": gin.H{"url": agentURL, "status": probe(agentURL + "/healthz")},
				"db":    gin.H{"path": cfg.Storage.SQLitePath},
			})
		})

		// Board (blackboard): facts / intents / hints + graph export.
		boardH := handler.NewBoardHandler(stores, hub)
		api.GET("/runs/:id/facts", boardH.ListFacts)
		api.POST("/runs/:id/facts", boardH.CreateFact)
		api.GET("/runs/:id/intents", boardH.ListIntents)
		api.POST("/runs/:id/intents", boardH.CreateIntent)
		api.POST("/runs/:id/intents/:iid/claim", boardH.ClaimIntent)
		api.POST("/runs/:id/intents/:iid/release", boardH.ReleaseIntent)
		api.POST("/runs/:id/intents/:iid/conclude", boardH.ConcludeIntent)
		api.POST("/runs/:id/intents/:iid/fail", boardH.FailIntent)
		api.GET("/runs/:id/hints", boardH.ListHints)
		api.POST("/runs/:id/hints", boardH.CreateHint)
		api.GET("/runs/:id/graph", boardH.Graph)
	}

	// SSE — registered outside the auth group so it can accept
	// `?token=...` directly. The handler itself re-checks the token
	// (EventSource can't set headers), so the token is passed in here.
	sseH := handler.NewSSEHandler(hub, stores, cfg.KeepAlive(), cfg.Admin.Token)
	router.GET("/api/runs/:id/events", sseH.Events)

	return router
}

// resolveAdminToken returns the admin bearer token, preferring (in order):
// YAML/env value -> persisted token from a previous boot -> a fresh random
// token that is persisted so restarts keep the same credential. An empty
// result would lock the whole API behind an unknown token, so this always
// ends up with a concrete value.
func resolveAdminToken(settings *store.SettingsStore, configured string) string {
	if configured != "" {
		return configured
	}
	if persisted, _ := settings.Get(context.Background(), handler.KeyAdminToken); persisted != "" {
		return persisted
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failing is fatal enough — fall back to a time-based
		// value rather than shipping a static default.
		return hex.EncodeToString([]byte(time.Now().UTC().String()))
	}
	tok := hex.EncodeToString(raw)
	_ = settings.Set(context.Background(), handler.KeyAdminToken, tok)
	return tok
}
