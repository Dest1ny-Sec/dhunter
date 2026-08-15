package handler

import (
	"net/http"
	"strings"

	"github.com/dhunter/dhunter/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// Persisted keys for the admin login credentials. Exported so cmd/ can reuse
// them during the first-run bootstrap.
const (
	KeyAdminUsername     = "admin_username"
	KeyAdminPasswordHash = "admin_password_hash"
)

// AuthHandler handles /api/auth/login, /api/auth/change and the admin
// bootstrap flow.
//
// The admin logs in with username + password and receives the static bearer
// token. Credentials are generated on first run (printed in the banner) and
// can be rotated later via /api/auth/change. Dhunter is a local tool, so
// there is no JWT, refresh flow, or session store.
type AuthHandler struct {
	AdminToken        string
	AdminUsername     string
	AdminPasswordHash string
	Settings          *store.SettingsStore
}

// NewAuthHandler constructs an AuthHandler. passwordHash may be empty — in
// that case login is disabled and the UI must use the token directly.
func NewAuthHandler(adminToken, adminUsername, passwordHash string, settings *store.SettingsStore) *AuthHandler {
	return &AuthHandler{
		AdminToken:        adminToken,
		AdminUsername:     adminUsername,
		AdminPasswordHash: passwordHash,
		Settings:          settings,
	}
}

// loginReq is the JSON body for POST /api/auth/login.
type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login handles POST /api/auth/login: check username + password, return the
// bearer token. The username must match the configured/generated admin
// username; the password must match the stored bcrypt hash.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if h.AdminPasswordHash == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "password login disabled; use admin token directly",
		})
		return
	}
	if req.Username == "" || req.Username != h.AdminUsername {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err := bcrypt.CompareHashAndPassword(
		[]byte(h.AdminPasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": h.AdminToken})
}

// changeReq is the JSON body for POST /api/auth/change (authenticated).
type changeReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Change handles POST /api/auth/change — rotate the admin login credentials.
// The static bearer token is unchanged. New credentials are persisted so they
// survive restarts. This route sits behind the Bearer middleware.
func (h *AuthHandler) Change(c *gin.Context) {
	var req changeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || len(req.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "username is required and password must be at least 6 characters",
		})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash failed"})
		return
	}
	ctx := c.Request.Context()
	if h.Settings != nil {
		if err := h.Settings.Set(ctx, KeyAdminUsername, username); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "persist failed"})
			return
		}
		if err := h.Settings.Set(ctx, KeyAdminPasswordHash, string(hash)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "persist failed"})
			return
		}
	}
	h.AdminUsername = username
	h.AdminPasswordHash = string(hash)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "账号已更新"})
}

// Healthz is a public liveness probe.
func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
