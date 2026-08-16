package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestLoginLimiterBlocksAfterMax(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	limiter := NewLoginLimiter(3, time.Minute)
	router.POST("/login", limiter.Middleware(), func(c *gin.Context) { c.Status(200) })

	var statuses []int
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		router.ServeHTTP(w, req)
		statuses = append(statuses, w.Code)
	}
	// 3 allowed, then 429.
	for i, s := range statuses {
		if i < 3 && s != 200 {
			t.Fatalf("attempt %d = %d, want 200", i, s)
		}
		if i >= 3 && s != http.StatusTooManyRequests {
			t.Fatalf("attempt %d = %d, want 429", i, s)
		}
	}
}

func TestLoginLimiterWindowSlides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	limiter := NewLoginLimiter(2, 50*time.Millisecond)
	router.POST("/login", limiter.Middleware(), func(c *gin.Context) { c.Status(200) })

	do := func() int {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))
		return w.Code
	}
	if do() != 200 || do() != 200 {
		t.Fatal("first two attempts should pass")
	}
	if do() != http.StatusTooManyRequests {
		t.Fatal("third attempt should be limited")
	}
	time.Sleep(60 * time.Millisecond) // window slides past the early attempts
	if do() != 200 {
		t.Fatal("after the window slides, attempts should be allowed again")
	}
}

func TestLoginLimiterDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	limiter := NewLoginLimiter(0, time.Minute) // max<=0 → no limit
	router.POST("/login", limiter.Middleware(), func(c *gin.Context) { c.Status(200) })

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))
		if w.Code != 200 {
			t.Fatalf("attempt %d = %d, want 200 (disabled limiter)", i, w.Code)
		}
	}
}
