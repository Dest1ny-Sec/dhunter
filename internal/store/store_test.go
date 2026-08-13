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

func TestVulnDedupAndStatusUpdate(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://x.com", Normalized: "x.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running"}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	v := &Vulnerability{RunID: run.ID, TargetID: tgt.ID, Title: "SQLi in login", Severity: "high",
		Target: "https://x.com/login", Evidence: "payload", Status: "pending"}
	if err := s.Vulns.Create(ctx, v); err != nil {
		t.Fatalf("create vuln: %v", err)
	}

	// exact duplicate is found
	dup, err := s.Vulns.FindDuplicate(ctx, run.ID, "SQLi in login", "https://x.com/login")
	if err != nil || dup != v.ID {
		t.Fatalf("expected duplicate id %s, got %s (err %v)", v.ID, dup, err)
	}
	// different title is not a duplicate
	dup, err = s.Vulns.FindDuplicate(ctx, run.ID, "Other", "https://x.com/login")
	if err != nil || dup != "" {
		t.Fatalf("expected no duplicate, got %q (err %v)", dup, err)
	}

	// status update
	if err := s.Vulns.UpdateStatus(ctx, v.ID, "confirmed"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, err := s.Vulns.Get(ctx, v.ID)
	if err != nil || got.Status != "confirmed" {
		t.Fatalf("status not updated: %+v (err %v)", got, err)
	}
}

func TestVulnCreateIfAbsentDedupsNormalizedTitle(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://x.com", Normalized: "x.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running"}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	mk := func(title string) *Vulnerability {
		return &Vulnerability{RunID: run.ID, TargetID: tgt.ID, Title: title,
			Severity: "high", Target: "https://x.com/api", Evidence: "p", Status: "pending"}
	}

	created, _, err := s.Vulns.CreateIfAbsent(ctx, mk("Unauthenticated access to /api/admin"))
	if err != nil || !created {
		t.Fatalf("first insert should create: created=%v err=%v", created, err)
	}
	// Near-identical title (different case/whitespace/punctuation) must dedup.
	created, existing, err := s.Vulns.CreateIfAbsent(ctx, mk("  Unauthenticated Access to /api/admin. "))
	if err != nil || created || existing == "" {
		t.Fatalf("normalized duplicate should dedup: created=%v existing=%q err=%v", created, existing, err)
	}
	// A genuinely different title must still be created.
	created, _, err = s.Vulns.CreateIfAbsent(ctx, mk("SQL injection in /api/login"))
	if err != nil || !created {
		t.Fatalf("distinct finding should create: created=%v err=%v", created, err)
	}
	vs, _ := s.Vulns.ListByRun(ctx, run.ID)
	if len(vs) != 2 {
		t.Fatalf("expected 2 vulns, got %d", len(vs))
	}
}

func TestVulnCreateIfAbsentDedups(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://x.com", Normalized: "x.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running"}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	mk := func() *Vulnerability {
		return &Vulnerability{RunID: run.ID, TargetID: tgt.ID, Title: "SQLi in login",
			Severity: "high", Target: "https://x.com/login", Evidence: "p", Status: "pending"}
	}

	created, existing, err := s.Vulns.CreateIfAbsent(ctx, mk())
	if err != nil || !created || existing != "" {
		t.Fatalf("first insert should create: created=%v existing=%q err=%v", created, existing, err)
	}
	created, existing, err = s.Vulns.CreateIfAbsent(ctx, mk())
	if err != nil || created || existing == "" {
		t.Fatalf("second insert should dedup: created=%v existing=%q err=%v", created, existing, err)
	}
	vs, _ := s.Vulns.ListByRun(ctx, run.ID)
	if len(vs) != 1 {
		t.Fatalf("expected 1 vuln after dedup, got %d", len(vs))
	}
}

func TestRunRecoverStale(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://x.com", Normalized: "x.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	r1 := &Run{TargetID: tgt.ID, Status: "running"}
	if err := s.Runs.Create(ctx, r1); err != nil {
		t.Fatalf("create run: %v", err)
	}
	r2 := &Run{TargetID: tgt.ID, Status: "completed"}
	if err := s.Runs.Create(ctx, r2); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := s.Runs.RecoverStale(ctx); err != nil {
		t.Fatalf("recover stale: %v", err)
	}
	g1, _ := s.Runs.Get(ctx, r1.ID)
	g2, _ := s.Runs.Get(ctx, r2.ID)
	if g1.Status != "failed" {
		t.Fatalf("stale run should be failed, got %s", g1.Status)
	}
	if g2.Status != "completed" {
		t.Fatalf("completed run must be untouched, got %s", g2.Status)
	}
}
