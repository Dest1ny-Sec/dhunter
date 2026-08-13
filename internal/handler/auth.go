package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// AuthHandler handles /api/auth/login and the admin bootstrap flow.
//
// MVP: a single static admin token. The login endpoint takes a password
// and, if it matches the configured bootstrap password, returns the
// token. The password is set on first run (printed to the banner).
type AuthHandler struct {
	AdminPasswordHash string
	AdminToken        string
}

// NewAuthHandler constructs an AuthHandler. passwordHash may be empty —
// in that case login is disabled and the UI must use the token directly.
func NewAuthHandler(adminToken, passwordHash string) *AuthHandler {
	return &AuthHandler{AdminToken: adminToken, AdminPasswordHash: passwordHash}
}

// loginReq is the JSON body for POST /api/auth/login.
type loginReq struct {
	Password string `json:"password"`
}

// Login handles POST /api/auth/login.
//
// The endpoint is intentionally simple: send the admin password, get
// the bearer token back. There is no JWT, no refresh flow, no session
// store — Dhunter is a local tool.
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
	if err := bcrypt.CompareHashAndPassword(
		[]byte(h.AdminPasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": h.AdminToken})
}

// Healthz is a public liveness probe.
func Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
