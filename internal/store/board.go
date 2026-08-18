// Board store: facts / intents / hints — the blackboard that multiple
// agent workers share. Facts are confirmed observations (append-only);
// intents are declared directions of exploration; hints are human input.
//
// Claim/Conclude use compare-and-swap style UPDATEs so concurrent workers
// can never double-claim an intent or double-conclude it.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/dhunter/dhunter/internal/db"
)

// Intent lifecycle states.
const (
	IntentOpen      = "open"
	IntentClaimed   = "claimed"
	IntentConcluded = "concluded"
	IntentFailed    = "failed"
)

// ErrIntentClaimed is returned when a claim races another worker.
var ErrIntentClaimed = errors.New("intent already claimed")

// Fact is one confirmed observation on the board.
type Fact struct {
	ID          string    `json:"id"`
	RunID       string    `json:"run_id"`
	Description string    `json:"description"`
	Source      string    `json:"source"`
	// Kind tags the observation: port / service / vuln / finding / http /
	// info. Confidence (0..1) marks how certain the evidence is — the
	// planner can weight low-confidence facts differently from hard proof.
	Kind       string    `json:"kind,omitempty"`
	Confidence float64   `json:"confidence,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Intent is one declared direction of exploration.
type Intent struct {
	ID          string     `json:"id"`
	RunID       string     `json:"run_id"`
	FromFacts   []string   `json:"from"`
	Description string     `json:"description"`
	Creator     string     `json:"creator"`
	Worker      *string    `json:"worker,omitempty"`
	Status      string     `json:"status"`
	ToFactID    *string    `json:"to_fact_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	ConcludedAt *time.Time `json:"concluded_at,omitempty"`
}

// Hint is human judgement injected mid-run.
type Hint struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Content   string    `json:"content"`
	Creator   string    `json:"creator"`
	CreatedAt time.Time `json:"created_at"`
}

// Board bundles the three board stores.
type Board struct {
	Facts   *FactStore
	Intents *IntentStore
	Hints   *HintStore
}

func newBoard(database *db.DB) *Board {
	return &Board{
		Facts:   &FactStore{db: database},
		Intents: &IntentStore{db: database},
		Hints:   &HintStore{db: database},
	}
}

// ----- FactStore -----

type FactStore struct{ db *db.DB }

func (s *FactStore) Create(ctx context.Context, f *Fact) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	if f.Kind == "" {
		f.Kind = "info"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO facts (id, run_id, description, source, kind, confidence, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.RunID, f.Description, f.Source, f.Kind, f.Confidence, f.CreatedAt.UnixMilli())
	return err
}

func (s *FactStore) ListByRun(ctx context.Context, runID string) ([]*Fact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, description, source, kind, confidence, created_at FROM facts WHERE run_id = ? ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Fact, 0)
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanFact(r rowScanner) (*Fact, error) {
	var f Fact
	var ms int64
	if err := r.Scan(&f.ID, &f.RunID, &f.Description, &f.Source, &f.Kind, &f.Confidence, &ms); err != nil {
		return nil, err
	}
	f.CreatedAt = time.UnixMilli(ms).UTC()
	return &f, nil
}

// ----- IntentStore -----

type IntentStore struct{ db *db.DB }

func (s *IntentStore) Create(ctx context.Context, i *Intent) error {
	if i.ID == "" {
		i.ID = uuid.NewString()
	}
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now().UTC()
	}
	if i.Status == "" {
		i.Status = IntentOpen
	}
	fromJSON, _ := json.Marshal(i.FromFacts)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO intents (id, run_id, from_facts, description, creator, worker, status, to_fact_id, created_at, claimed_at, concluded_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.RunID, string(fromJSON), i.Description, i.Creator, i.Worker, i.Status, i.ToFactID,
		i.CreatedAt.UnixMilli(), nullableMillis(i.ClaimedAt), nullableMillis(i.ConcludedAt))
	return err
}

func (s *IntentStore) Get(ctx context.Context, runID, id string) (*Intent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, run_id, from_facts, description, creator, worker, status, to_fact_id, created_at, claimed_at, concluded_at
		 FROM intents WHERE run_id = ? AND id = ?`, runID, id)
	return scanIntent(row)
}

func (s *IntentStore) ListByRun(ctx context.Context, runID string) ([]*Intent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, from_facts, description, creator, worker, status, to_fact_id, created_at, claimed_at, concluded_at
		 FROM intents WHERE run_id = ? ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Intent, 0)
	for rows.Next() {
		i, err := scanIntent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// OpenIntents returns intents that are not yet concluded or failed.
func (s *IntentStore) OpenIntents(ctx context.Context, runID string) ([]*Intent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, from_facts, description, creator, worker, status, to_fact_id, created_at, claimed_at, concluded_at
		 FROM intents WHERE run_id = ? AND status IN ('open','claimed') ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Intent, 0)
	for rows.Next() {
		i, err := scanIntent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Claim is a CAS: only an unclaimed open intent can be claimed. Returns
// ErrIntentClaimed if another worker got there first.
func (s *IntentStore) Claim(ctx context.Context, runID, id, worker string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE intents SET worker = ?, status = ?, claimed_at = ? WHERE run_id = ? AND id = ? AND status = ? AND worker IS NULL`,
		worker, IntentClaimed, time.Now().UTC().UnixMilli(), runID, id, IntentOpen)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrIntentClaimed
	}
	return nil
}

// Release returns a claimed intent to open (worker gave up before concluding).
func (s *IntentStore) Release(ctx context.Context, runID, id, worker string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE intents SET worker = NULL, status = ? WHERE run_id = ? AND id = ? AND worker = ?`,
		IntentOpen, runID, id, worker)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Fail marks a claimed intent as failed (dead-end exploration).
func (s *IntentStore) Fail(ctx context.Context, runID, id, worker string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE intents SET status = ?, concluded_at = ? WHERE run_id = ? AND id = ? AND worker = ?`,
		IntentFailed, time.Now().UTC().UnixMilli(), runID, id, worker)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Conclude atomically creates a fact and resolves the intent to it. The
// mutation happens in one transaction so the graph can never show an
// intent pointing at a fact that doesn't exist.
func (s *IntentStore) Conclude(ctx context.Context, runID, id, worker, description string) (*Fact, error) {
	// Only the current worker may conclude; re-check status under txn.
	f := &Fact{RunID: runID, Description: description, Source: "intent:" + id, CreatedAt: time.Now().UTC()}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE intents SET status = ?, to_fact_id = ?, concluded_at = ? WHERE run_id = ? AND id = ? AND worker = ?`,
		IntentConcluded, f.ID, now.UnixMilli(), runID, id, worker)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO facts (id, run_id, description, source, kind, confidence, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.RunID, f.Description, f.Source, "info", 0.5, now.UnixMilli()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

func scanIntent(r rowScanner) (*Intent, error) {
	var (
		i         Intent
		fromJSON  string
		worker    sql.NullString
		toFact    sql.NullString
		createdMs int64
		claimedMs sql.NullInt64
		conclMs   sql.NullInt64
	)
	if err := r.Scan(&i.ID, &i.RunID, &fromJSON, &i.Description, &i.Creator, &worker,
		&i.Status, &toFact, &createdMs, &claimedMs, &conclMs); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(fromJSON), &i.FromFacts)
	if i.FromFacts == nil {
		i.FromFacts = []string{}
	}
	if worker.Valid {
		i.Worker = &worker.String
	}
	if toFact.Valid {
		i.ToFactID = &toFact.String
	}
	i.CreatedAt = time.UnixMilli(createdMs).UTC()
	if claimedMs.Valid {
		t := time.UnixMilli(claimedMs.Int64).UTC()
		i.ClaimedAt = &t
	}
	if conclMs.Valid {
		t := time.UnixMilli(conclMs.Int64).UTC()
		i.ConcludedAt = &t
	}
	return &i, nil
}

// ----- HintStore -----

type HintStore struct{ db *db.DB }

func (s *HintStore) Create(ctx context.Context, h *Hint) error {
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO hints (id, run_id, content, creator, created_at) VALUES (?, ?, ?, ?, ?)`,
		h.ID, h.RunID, h.Content, h.Creator, h.CreatedAt.UnixMilli())
	return err
}

func (s *HintStore) ListByRun(ctx context.Context, runID string) ([]*Hint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, content, creator, created_at FROM hints WHERE run_id = ? ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Hint, 0)
	for rows.Next() {
		var h Hint
		var ms int64
		if err := rows.Scan(&h.ID, &h.RunID, &h.Content, &h.Creator, &ms); err != nil {
			return nil, err
		}
		h.CreatedAt = time.UnixMilli(ms).UTC()
		out = append(out, &h)
	}
	return out, rows.Err()
}
