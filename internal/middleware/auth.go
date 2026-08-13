// Package middleware contains Gin middlewares shared by the API.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CtxAdminToken is the gin.Context key under which the verified admin
// bearer token is stored. Handlers can read it via c.GetString.
const CtxAdminToken = "dhunter.admin.token"

// Bearer enforces a single static admin token. We accept it via the
// `Authorization: Bearer <token>` header or a `?token=` query parameter
// (the latter is convenient for SSE endpoints that browsers can't easily
// set custom headers on).
//
// The token is intentionally simple: Dhunter is a local-first tool, and
// the bearer secret is what gates the API. A future v0.2 swap to JWT or
// per-user accounts is straightforward because handlers never call this
// directly — they trust the CtxAdminToken value.
func Bearer(adminToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublic(c.Request.URL.Path) {
			c.Next()
			return
		}
		token := extractToken(c)
		if token == "" || token != adminToken {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
				"hint":  "send `Authorization: Bearer <admin_token>`",
			})
			return
		}
		c.Set(CtxAdminToken, token)
		c.Next()
	}
}

// isPublic returns true for paths the auth middleware must skip.
func isPublic(path string) bool {
	switch path {
	case "/api/healthz", "/api/auth/login", "/":
		return true
	}
	// SSE is gated separately by per-run subscription tokens; the
	// middleware stays out of its way.
	if strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/events") {
		return true
	}
	return false
}

// extractToken reads the bearer token from header or query string.
func extractToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); h != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(h, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(h, prefix))
		}
	}
	return strings.TrimSpace(c.Query("token"))
}
