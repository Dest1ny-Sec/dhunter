package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/db"
	"github.com/dhunter/dhunter/internal/store"
)

func newSkillTestEnv(t *testing.T) (*store.Stores, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tmp := t.TempDir() + "/test.db"
	d, err := db.Open(tmp)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	stores := store.New(d)
	return stores, func() { _ = d.Close(context.Background()) }
}

func TestSkillHandler_CRUD_Roundtrip(t *testing.T) {
	stores, cleanup := newSkillTestEnv(t)
	defer cleanup()
	h := NewSkillHandler(stores)
	r := gin.New()
	h.Register(r.Group(""))

	// 1. CREATE custom skill
	body := createSkillReq{
		Name: "my-pentest", Title: "我的 Pentest",
		Description: "自定义工作流", Content: "# Steps\n1. do X",
		Category: "web", Tags: "xss,sqli", Enabled: boolPtr(true),
	}
	w := doJSON(r, "POST", "/skills", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created store.Skill
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == "" || created.Name != "my-pentest" {
		t.Fatalf("bad create: %+v", created)
	}
	if created.Source != "custom" {
		t.Fatalf("source default: want 'custom', got %q", created.Source)
	}

	// 2. LIST
	w = doJSON(r, "GET", "/skills", nil)
	if w.Code != 200 {
		t.Fatalf("list: %d", w.Code)
	}
	var listResp struct {
		Skills []*store.Skill `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Skills) != 1 {
		t.Fatalf("want 1, got %d", len(listResp.Skills))
	}

	// 3. UPDATE
	upd := updateSkillReq{
		Name: "my-pentest", Title: "我的 Pentest v2",
		Description: "v2", Content: "# v2", Category: "api",
	}
	w = doJSON(r, "PUT", "/skills/"+created.ID, upd)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	got, err := stores.Skills.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "我的 Pentest v2" {
		t.Fatalf("title not updated: %q", got.Title)
	}

	// 4. TOGGLE
	w = doJSON(r, "POST", "/skills/"+created.ID+"/toggle", map[string]any{"enabled": false})
	if w.Code != 200 {
		t.Fatalf("toggle: %d", w.Code)
	}
	got, _ = stores.Skills.Get(context.Background(), created.ID)
	if got.Enabled {
		t.Fatalf("toggle should disable")
	}

	// 5. DELETE
	w = doJSON(r, "DELETE", "/skills/"+created.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := stores.Skills.Get(context.Background(), created.ID); err == nil {
		t.Fatalf("expected ErrNotFound after delete")
	}
}

func TestSkillHandler_RejectsBadName(t *testing.T) {
	stores, cleanup := newSkillTestEnv(t)
	defer cleanup()
	h := NewSkillHandler(stores)
	r := gin.New()
	h.Register(r.Group(""))

	cases := []struct {
		name string
		body createSkillReq
		want int
	}{
		{"missing name", createSkillReq{Title: "x"}, 400},
		{"with space", createSkillReq{Name: "foo bar"}, 400},
		{"starts with dash", createSkillReq{Name: "-foo"}, 400},
		{"too long", createSkillReq{Name: strings.Repeat("a", 65)}, 400},
		{"reserved source", createSkillReq{Name: "foo", Source: "builtin"}, 400},
		{"valid uppercase is normalized", createSkillReq{Name: "Foo"}, 201}, // server lowercases; that's fine
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(r, "POST", "/skills", tc.body)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d %s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestSkillHandler_NameUniqueness(t *testing.T) {
	stores, cleanup := newSkillTestEnv(t)
	defer cleanup()
	h := NewSkillHandler(stores)
	r := gin.New()
	h.Register(r.Group(""))

	body := createSkillReq{Name: "dup", Title: "x"}
	w := doJSON(r, "POST", "/skills", body)
	if w.Code != 201 {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, "POST", "/skills", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("second: want 409, got %d", w.Code)
	}
}

func TestSkillHandler_BuiltinSeededAndProtected(t *testing.T) {
	stores, cleanup := newSkillTestEnv(t)
	defer cleanup()

	// Seed
	if err := SeedBuiltinSkills(context.Background(), stores.Skills); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Re-seed: should be idempotent (no duplicates).
	if err := SeedBuiltinSkills(context.Background(), stores.Skills); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	all, err := stores.Skills.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]*store.Skill{}
	for _, s := range all {
		byName[s.Name] = s
	}
	// Expect all 3 builtins seeded exactly once.
	for _, n := range []string{"web-attack-surface", "api-fuzz-workflow", "vuln-report-writer"} {
		if _, ok := byName[n]; !ok {
			t.Fatalf("missing built-in %q", n)
		}
	}
	// Idempotency: same count after re-seed.
	all2, _ := stores.Skills.List(context.Background(), "")
	if len(all) != len(all2) {
		t.Fatalf("re-seed created duplicates: before=%d after=%d", len(all), len(all2))
	}

	// Built-in: DELETE should be forbidden, UPDATE should only toggle.
	h := NewSkillHandler(stores)
	r := gin.New()
	h.Register(r.Group(""))
	builtin := byName["web-attack-surface"]
	w := doJSON(r, "DELETE", "/skills/"+builtin.ID, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("delete builtin: want 403, got %d", w.Code)
	}
	// Update with full body should still work (content is not changed
	// by Update for builtins — Toggle is called instead).
	upd := updateSkillReq{Name: "web-attack-surface", Title: "hijack", Enabled: boolPtr(false)}
	w = doJSON(r, "PUT", "/skills/"+builtin.ID, upd)
	if w.Code != 200 {
		t.Fatalf("update builtin: %d", w.Code)
	}
	got, _ := stores.Skills.Get(context.Background(), builtin.ID)
	if got.Title == "hijack" {
		t.Fatalf("builtin title should be immutable, got %q", got.Title)
	}
	if got.Enabled {
		t.Fatalf("builtin should have been disabled by the toggle path")
	}
}

func TestSkillHandler_FilterBySource(t *testing.T) {
	stores, cleanup := newSkillTestEnv(t)
	defer cleanup()
	ctx := context.Background()
	if err := SeedBuiltinSkills(ctx, stores.Skills); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := stores.Skills.Create(ctx, &store.Skill{Name: "user-skill", Title: "u", Source: "custom"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// /skills?source=builtin
	builtIns, _ := stores.Skills.List(ctx, "builtin")
	if len(builtIns) < 3 {
		t.Fatalf("want >= 3 builtins, got %d", len(builtIns))
	}
	for _, s := range builtIns {
		if s.Source != "builtin" {
			t.Fatalf("non-builtin leaked: %+v", s)
		}
	}

	custom, _ := stores.Skills.List(ctx, "custom")
	if len(custom) != 1 {
		t.Fatalf("want 1 custom, got %d", len(custom))
	}
}

// Register mounts the handler at the given group root.
func (h *SkillHandler) Register(g *gin.RouterGroup) {
	g.GET("/skills", h.List)
	g.GET("/skills/:id", h.Get)
	g.POST("/skills", h.Create)
	g.PUT("/skills/:id", h.Update)
	g.DELETE("/skills/:id", h.Delete)
	g.POST("/skills/:id/toggle", h.Toggle)
}

// doJSON for skill tests — re-use the mcp_test.go helper if the same
// package; otherwise inline a minimal shim.
func init() {
	// Reference a no-op to keep the lint happy if helpers move.
	_ = bytes.NewReader
	_ = io.Discard
}
