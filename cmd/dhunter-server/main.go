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

	// --- Bootstrap admin account (stable across restarts) ----------
	adminUser, passwordHash, generated, err := bootstrapAdmin(cfg, stores.Settings)
	if err != nil {
		log.Fatalf("dhunter: bootstrap admin: %v", err)
	}
	// Admin bearer token: configured value, else persisted, else a fresh
	// random one persisted to the settings table. Never a static default.
	cfg.Admin.Token = resolveAdminToken(stores.Settings, cfg.Admin.Token)
	printBanner(cfg, generated)

	// --- Stores / hub / agent bridge -------------------------------
	hub := stream.New()
	bridge := agent.New(cfg.Agent.PythonURL, cfg.Agent.Token, stores, hub)

	// --- Router (API + SSE + SPA) ----------------------------------
	router := buildRouter(cfg, stores, hub, bridge, adminUser, passwordHash)

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

// bootstrapAdmin resolves the admin login credentials (username + password
// hash) used by /api/auth/login.
//
// Precedence:
//  1. force_reset_password: true → bootstrap_password OVERWRITES the
//     persisted hash (the recovery path for a lost password). Refuses to
//     auto-generate: without an explicit bootstrap_password the server
//     fails fast instead of silently rotating the credential.
//  2. PERSISTED credentials win (first-run generation or a Settings
//     rotation), so changes survive restarts.
//  3. A YAML bootstrap_password only SEEDS the very first run (and is then
//     persisted). If neither is present a fresh random password is
//     generated and printed in the banner exactly once.
func bootstrapAdmin(cfg *config.Config, settings *store.SettingsStore) (username, hash string, generated bool, err error) {
	ctx := context.Background()

	// Resolve the username: a persisted one wins, else the configured default.
	username = cfg.Admin.Username
	if username == "" {
		username = "admin"
	}
	if persistedUser, _ := settings.Get(ctx, handler.KeyAdminUsername); persistedUser != "" {
		username = persistedUser
	}
	cfg.Admin.Username = username

	// Explicit force reset — the lost-password recovery path.
	if cfg.Admin.ForceResetPassword {
		plain := cfg.Admin.BootstrapPassword
		if plain == "" {
			return "", "", false, errors.New("force_reset_password: true requires bootstrap_password to be set (refusing to auto-generate a password)")
		}
		h, herr := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
		if herr != nil {
			return "", "", false, herr
		}
		if err := settings.Set(ctx, handler.KeyAdminPasswordHash, string(h)); err != nil {
			return "", "", false, err
		}
		if err := settings.Set(ctx, handler.KeyAdminUsername, username); err != nil {
			return "", "", false, err
		}
		cfg.Admin.BootstrapPassword = plain
		log.Printf("dhunter: admin credentials force-reset (force_reset_password=true) — remove the flag after confirming login")
		return username, string(h), false, nil
	}

	// Persisted credentials win — a Settings rotation must survive restarts.
	if existing, _ := settings.Get(ctx, handler.KeyAdminPasswordHash); existing != "" {
		cfg.Admin.BootstrapPassword = "[persisted]"
		return username, existing, false, nil
	}

	// Seed the very first run: configured YAML password, else a random one.
	plain := cfg.Admin.BootstrapPassword
	if plain == "" {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return "", "", false, err
		}
		plain = hex.EncodeToString(raw)
		generated = true
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", "", false, err
	}
	if err := settings.Set(ctx, handler.KeyAdminPasswordHash, string(h)); err != nil {
		return "", "", false, err
	}
	if err := settings.Set(ctx, handler.KeyAdminUsername, username); err != nil {
		return "", "", false, err
	}
	// Stash the plaintext on the config so the banner can print it.
	cfg.Admin.BootstrapPassword = plain
	return username, string(h), generated, nil
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
			fmt.Sprintf("    username: %s", cfg.Admin.Username),
			fmt.Sprintf("    password: %s", cfg.Admin.BootstrapPassword),
			"    token:    "+cfg.Admin.Token,
			"    (login with username/password, then change them in Settings)",
		)
	} else {
		banner = append(banner,
			"  ADMIN",
			fmt.Sprintf("    username: %s", cfg.Admin.Username),
			"    password: [set — rotate via Settings or YAML bootstrap_password]",
			"    token:    "+cfg.Admin.Token,
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
