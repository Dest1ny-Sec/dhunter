package handler

import (
	"net/http"

	"github.com/dhunter/dhunter/internal/store"
	"github.com/gin-gonic/gin"
)

// SearchHandler exposes full-text search over agent conversation history.
type SearchHandler struct {
	Stores *store.Stores
}

// NewSearchHandler constructs a SearchHandler.
func NewSearchHandler(stores *store.Stores) *SearchHandler {
	return &SearchHandler{Stores: stores}
}

// Messages handles GET /api/search/messages?q=...
func (h *SearchHandler) Messages(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"hits": []*store.MessageHit{}})
		return
	}
	hits, err := h.Stores.Messages.Search(c.Request.Context(), q, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hits": hits})
}
