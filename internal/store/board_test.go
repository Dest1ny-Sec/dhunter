package store

import (
	"context"
	"testing"
)

func TestBoardIntentClaimCASAndConclude(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	// seed target + run
	tgt := &Target{Type: "url", Value: "https://example.com", Normalized: "example.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running"}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	it := &Intent{
		RunID:       run.ID,
		FromFacts:   []string{"origin"},
		Description: "enumerate subdomains",
		Creator:     "reason",
	}
	if err := s.Board.Intents.Create(ctx, it); err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if it.Status != IntentOpen {
		t.Fatalf("expected open, got %s", it.Status)
	}

	// claim by worker A succeeds
	if err := s.Board.Intents.Claim(ctx, run.ID, it.ID, "worker-a"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// claim by worker B must fail (CAS)
	if err := s.Board.Intents.Claim(ctx, run.ID, it.ID, "worker-b"); err != ErrIntentClaimed {
		t.Fatalf("expected ErrIntentClaimed, got %v", err)
	}

	// conclude by the wrong worker must fail
	if _, err := s.Board.Intents.Conclude(ctx, run.ID, it.ID, "worker-b", "stolen"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for wrong worker, got %v", err)
	}

	// conclude by the owner produces a fact and resolves the intent
	f, err := s.Board.Intents.Conclude(ctx, run.ID, it.ID, "worker-a", "found 12 subdomains")
	if err != nil {
		t.Fatalf("conclude: %v", err)
	}
	got, err := s.Board.Intents.Get(ctx, run.ID, it.ID)
	if err != nil {
		t.Fatalf("get intent: %v", err)
	}
	if got.Status != IntentConcluded || got.ToFactID == nil || *got.ToFactID != f.ID {
		t.Fatalf("intent not concluded properly: %+v", got)
	}

	// fact is visible
	facts, err := s.Board.Facts.ListByRun(ctx, run.ID)
	if err != nil || len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d (err %v)", len(facts), err)
	}
	if facts[0].Description != "found 12 subdomains" {
		t.Fatalf("unexpected fact: %+v", facts[0])
	}
}

func TestBoardReleaseReturnsToOpen(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://example.com", Normalized: "example.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running"}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	it := &Intent{RunID: run.ID, Description: "x", Creator: "reason"}
	if err := s.Board.Intents.Create(ctx, it); err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if err := s.Board.Intents.Claim(ctx, run.ID, it.ID, "w"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.Board.Intents.Release(ctx, run.ID, it.ID, "w"); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, _ := s.Board.Intents.Get(ctx, run.ID, it.ID)
	if got.Status != IntentOpen || got.Worker != nil {
		t.Fatalf("release did not return to open: %+v", got)
	}
	// can be re-claimed
	if err := s.Board.Intents.Claim(ctx, run.ID, it.ID, "w2"); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
}

func TestBoardHintsCreateAndList(t *testing.T) {
	s, cleanup := newTestStores(t)
	defer cleanup()
	ctx := context.Background()

	tgt := &Target{Type: "url", Value: "https://example.com", Normalized: "example.com"}
	if err := s.Targets.Create(ctx, tgt); err != nil {
		t.Fatalf("create target: %v", err)
	}
	run := &Run{TargetID: tgt.ID, Status: "running"}
	if err := s.Runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	h := &Hint{RunID: run.ID, Content: "try /actuator/env", Creator: "operator"}
	if err := s.Board.Hints.Create(ctx, h); err != nil {
		t.Fatalf("create hint: %v", err)
	}
	hs, err := s.Board.Hints.ListByRun(ctx, run.ID)
	if err != nil || len(hs) != 1 || hs[0].Content != "try /actuator/env" {
		t.Fatalf("hint roundtrip failed: %+v (err %v)", hs, err)
	}
}
