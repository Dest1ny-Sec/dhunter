package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhunter/dhunter/internal/db"
)

// newTestStores opens a throwaway SQLite DB in a temp dir, runs migrations,
// and returns the Stores wrapper plus a cleanup func.
func newTestStores(t *testing.T) (*Stores, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	ctx := context.Background()
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return New(d), func() {
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = d.Close(cctx)
	}
}

func TestRunStoreAddTokensAccumulates(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://example.com", Normalized: "example.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running", StartedAt: time.Now().UTC()}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := s.Runs.AddTokens(ctx, run.ID, 100, 20, 50, 30); err != nil {
		t.Fatalf("add tokens: %v", err)
	}
	if err := s.Runs.AddTokens(ctx, run.ID, 50, 10, 0, 0); err != nil {
		t.Fatalf("add tokens: %v", err)
	}

	got, err := s.Runs.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.InputTokens != 150 || got.OutputTokens != 30 || got.CacheCreationInputTokens != 50 || got.CacheReadInputTokens != 30 {
		t.Fatalf("tokens not accumulated: %+v", got)
	}
}

func TestVulnStoreCreateAndFilter(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://juice-sh.op", Normalized: "juice-sh.op"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running", StartedAt: time.Now().UTC()}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	v := &Vulnerability{
		RunID:    run.ID,
		TargetID: tgt.ID,
		Title:    "SQL Injection in login",
		Severity: "critical",
		Status:   "open",
		Target:   "https://juice-sh.op/rest/user/login",
		Evidence: "curl -X POST ...",
	}
	if err := s.Vulns.Create(ctx, v); err != nil {
		t.Fatalf("create vuln: %v", err)
	}

	// Filter by run
	vs, err := s.Vulns.ListAll(ctx, run.ID, "", "")
	if err != nil || len(vs) != 1 {
		t.Fatalf("list by run: got %d, err %v", len(vs), err)
	}
	// Filter by severity
	vs, err = s.Vulns.ListAll(ctx, "", "", "critical")
	if err != nil || len(vs) != 1 {
		t.Fatalf("list by severity: got %d, err %v", len(vs), err)
	}
	vs, err = s.Vulns.ListAll(ctx, "", "", "low")
	if err != nil || len(vs) != 0 {
		t.Fatalf("list by severity low: got %d, err %v", len(vs), err)
	}
}

func TestTargetGetNotFound(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := s.Targets.Get(ctx, "does-not-exist"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
