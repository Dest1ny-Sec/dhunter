// Command dhunter-server is the Dhunter API + SSE host.
//
// It wires the Gin router, opens the SQLite database, runs migrations,
// and starts the HTTP listener with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/dhunter/dhunter/internal/agent"
	"github.com/dhunter/dhunter/internal/config"
	"github.com/dhunter/dhunter/internal/db"
	"github.com/dhunter/dhunter/internal/handler"
	"github.com/dhunter/dhunter/internal/middleware"
	"github.com/dhunter/dhunter/internal/store"
	"github.com/dhunter/dhunter/internal/stream"
)

func main() {
	var (
		cfgPath = flag.String("config", "./configs/dhunter.yaml", "path to YAML config")
		port    = flag.Int("port", 0, "override server port (env DHUNTER_PORT also works)")
		httpOn  = flag.Bool("http", false, "alias: explicitly opt in to plain HTTP (default)")
	)
	flag.Parse()
	_ = httpOn // accepted for forward-compat; HTTPS is out of MVP scope

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("dhunter: load config: %v", err)
	}
	if *port > 0 {
		cfg.Server.Port = *port
	}

	// --- Database --------------------------------------------------
	database, err := db.Open(cfg.Storage.SQLitePath)
	if err != nil {
		log.Fatalf("dhunter: open db: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = database.Close(shutdownCtx)
	}()

	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 30*time.Second)
	if err := database.Migrate(bootCtx); err != nil {
		cancelBoot()
		log.Fatalf("dhunter: migrate: %v", err)
	}
	// Crash recovery: runs left "running" by a previous process have no
	// live agent session — mark them failed instead of hanging forever.
	stores := store.New(database)
	if err := stores.Runs.RecoverStale(bootCtx); err != nil {
		log.Printf("dhunter: warn: recover stale runs: %v", err)
	}
	cancelBoot()

	// --- Bootstrap admin password (stable across restarts) ---------
	passwordHash, generated, err := bootstrapAdmin(cfg, stores.Settings)
	if err != nil {
		log.Fatalf("dhunter: bootstrap admin: %v", err)
	}
	printBanner(cfg, generated)

	// --- Stores / hub / agent bridge -------------------------------
	hub := stream.New()
	bridge := agent.New(cfg.Agent.PythonURL, stores, hub)

	// --- Router ----------------------------------------------------
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())
	router.Use(corsForOrigins(cfg.Server.AllowedOrigins))

	mountWebUI(router)
	router.GET("/api/healthz", handler.Healthz)
	router.POST("/api/auth/login", handler.NewAuthHandler(cfg.Admin.Token, passwordHash).Login)

	// Authenticated API group.
	api := router.Group("/api")
	api.Use(middleware.Bearer(cfg.Admin.Token))
	{
		targetH := handler.NewTargetHandler(stores)
		api.POST("/targets", targetH.Create)
		api.GET("/targets", targetH.List)
		api.GET("/targets/:id", targetH.Get)
		api.PATCH("/targets/:id/auth", targetH.SetAuth)
		api.PATCH("/targets/:id/redlines", targetH.SetRedLines)
		api.DELETE("/targets/:id", targetH.Delete)

		runH := handler.NewRunHandler(stores, bridge, hub)
		api.POST("/runs", runH.Create)
		api.POST("/runs/:id/cancel", runH.Cancel)

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
		api.GET("/knowledge", settingsH.KnowledgeList)
		api.POST("/knowledge", settingsH.KnowledgeAdd)

		api.GET("/status", func(c *gin.Context) {
			hc := &http.Client{Timeout: 3 * time.Second}
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
				"mcp":   gin.H{"url": cfg.MCP.WebHunter.URL, "status": probe(mcpBase + "/health"), "tools": listMCPTools(cfg.MCP.WebHunter.URL, cfg.MCP.WebHunter.Token)},
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

	// --- Server + graceful shutdown --------------------------------
	// Binds 127.0.0.1 by default. For remote/team access, set
	// `server.host: 0.0.0.0` explicitly (and put TLS in front).
	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout is reset to zero per-request for SSE streams (see the
		// SSE handler); 60s guards regular API endpoints against slow clients.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("dhunter: listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("dhunter: server: %v", err)
		}
	}()

	<-stop
	log.Printf("dhunter: shutdown signal received")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("dhunter: graceful shutdown failed: %v", err)
	}
	log.Printf("dhunter: bye")
}

// bootstrapAdmin returns the bcrypt hash to use for the admin password.
// If a bootstrap password is configured in YAML, we hash it. Otherwise we
// reuse a previously generated password (persisted in the settings table)
// so restarts don't invalidate the operator's session; only the very first
// run generates and prints a fresh random password.
func bootstrapAdmin(cfg *config.Config, settings *store.SettingsStore) (hash string, generated bool, err error) {
	const key = "admin_password_hash"
	ctx := context.Background()

	if cfg.Admin.BootstrapPassword != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(cfg.Admin.BootstrapPassword), bcrypt.DefaultCost)
		if err != nil {
			return "", false, err
		}
		return string(h), false, nil
	}
	// Reuse the stored hash if it exists (stable password across restarts).
	if existing, _ := settings.Get(ctx, key); existing != "" {
		cfg.Admin.BootstrapPassword = "[persisted]"
		return existing, false, nil
	}
	// First run: generate, persist the hash, and print the plaintext once.
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", false, err
	}
	plain := hex.EncodeToString(raw)
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", false, err
	}
	if err := settings.Set(ctx, key, string(h)); err != nil {
		return "", false, err
	}
	// Stash the plaintext on the config so the banner can print it.
	cfg.Admin.BootstrapPassword = plain
	return string(h), true, nil
}

// printBanner dumps the multi-line startup banner. The format is
// deliberately fixed-width so it reads the same in a terminal and in a
// log file.
func printBanner(cfg *config.Config, adminGenerated bool) {
	addr := fmt.Sprintf("http://127.0.0.1:%d/", cfg.Server.Port)
	banner := []string{
		"╔════════════════════════════════════════════════════════╗",
		"║                       Dhunter                          ║",
		"║          AI-driven web penetration testing             ║",
		"╚════════════════════════════════════════════════════════╝",
		"",
		fmt.Sprintf("  ONLINE  %s", addr),
		fmt.Sprintf("  AGENT   %s", cfg.Agent.PythonURL),
		fmt.Sprintf("  TOOLS   %s", cfg.MCP.WebHunter.URL),
		fmt.Sprintf("  LLM     %s / %s", cfg.LLM.Provider, cfg.LLM.Model),
		fmt.Sprintf("  STORE   %s", cfg.Storage.SQLitePath),
		"",
	}
	if adminGenerated {
		banner = append(banner,
			"  ADMIN SETUP REQUIRED",
			fmt.Sprintf("    password: %s", cfg.Admin.BootstrapPassword),
			"    token:    "+cfg.Admin.Token,
			"    (change both in configs/dhunter.yaml before exposing the API)",
		)
	} else {
		banner = append(banner,
			"  ADMIN",
			"    using configured bootstrap password (set in YAML)",
			"    token: "+cfg.Admin.Token,
		)
	}
	for _, line := range banner {
		fmt.Println(line)
	}
}

// requestLogger is a minimal access log. The default gin.Logger writes
// to stdout with ANSI colour codes; we want a single clean line per
// request.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Printf("dhunter: %s %s %d %s",
			c.Request.Method, c.Request.URL.Path,
			c.Writer.Status(), time.Since(start).Truncate(time.Microsecond))
	}
}

// corsForOrigins restricts CORS to the configured origins. With no origins
// configured (the default single-port deployment) no CORS headers are sent
// at all — same-origin requests work fine and cross-origin is denied.
func corsForOrigins(allowed []string) gin.HandlerFunc {
	allowedSet := map[string]struct{}{}
	for _, o := range allowed {
		allowedSet[o] = struct{}{}
	}
	return func(c *gin.Context) {
		if len(allowedSet) > 0 {
			origin := c.GetHeader("Origin")
			if _, ok := allowedSet[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
				c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			}
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

// pathExists is a small helper used by tests / setup scripts.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

// mountWebUI serves the Vite-built SPA from <repo>/frontend/dist (if it
// exists). The API owns `/api/*`; everything else is treated as a Vue
// route and falls back to `index.html` so client-side routing works.
func mountWebUI(router *gin.Engine) {
	candidates := []string{
		"./frontend/dist",
		"./web",
		"./dist",
		"./public",
	}
	exeDir, _ := os.Getwd()
	for _, rel := range candidates {
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(exeDir, rel)
		}
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			indexPath := filepath.Join(abs, "index.html")
			if _, err := os.Stat(indexPath); err != nil {
				continue
			}
			fs := http.FileServer(http.Dir(abs))
			router.NoRoute(func(c *gin.Context) {
				p := c.Request.URL.Path
				if strings.HasPrefix(p, "/api/") {
					c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
					return
				}
				// Serve real file if it exists; otherwise hand back
				// index.html so the Vue router can take over.
				full := filepath.Join(abs, filepath.Clean(p))
				if p != "/" {
					if st, err := os.Stat(full); err == nil && !st.IsDir() {
						fs.ServeHTTP(c.Writer, c.Request)
						return
					}
				}
				c.File(indexPath)
			})
			log.Printf("dhunter: web UI mounted at %s", abs)
			return
		}
	}
	log.Printf("dhunter: no web UI found (frontend/dist not built) — only API is served")
}

var _ = filepath.Join // keep filepath import if future flags need it

// listMCPTools queries the bundled MCP server for its tool names (used by
// the Settings page to show the toolbelt).
func listMCPTools(mcpURL, token string) []string {
	type rpcReq struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
	}
	body, _ := json.Marshal(rpcReq{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	req, _ := http.NewRequest("POST", mcpURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	names := make([]string, 0, len(out.Result.Tools))
	for _, t := range out.Result.Tools {
		names = append(names, t.Name)
	}
	return names
}
