// Package store is the thin data-access layer above *db.DB.
//
// Stores do not own the database — they're constructed with one and
// hand raw SQL out to the caller. JSON-ish columns (attributes, payload)
// are stored as TEXT and exposed as strings; the handler layer is
// responsible for serialising/deserialising those.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/dhunter/dhunter/internal/db"
)

// ErrNotFound is returned by Get* methods when no row matches.
var ErrNotFound = errors.New("not found")

// Target is one of: company, domain, url, ip.
type Target struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Value      string          `json:"value"`
	Normalized string          `json:"normalized"`
	Attributes json.RawMessage `json:"attributes"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Run is a single agent execution.
type Run struct {
	ID         string     `json:"id"`
	TargetID   string     `json:"target_id"`
	Objective  string     `json:"objective"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	// Cumulative LLM token usage across the run.
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Message is one streamed event from the agent.
type Message struct {
	ID        string          `json:"id"`
	RunID     string          `json:"run_id"`
	Role      string          `json:"role"`
	EventType string          `json:"event_type"`
	Content   string          `json:"content"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// Vulnerability is a confirmed issue.
type Vulnerability struct {
	ID             string    `json:"id"`
	RunID          string    `json:"run_id"`
	TargetID       string    `json:"target_id"`
	Title          string    `json:"title"`
	Severity       string    `json:"severity"`
	Status         string    `json:"status"`
	Target         string    `json:"target"`
	Evidence       string    `json:"evidence"`
	Impact         string    `json:"impact"`
	Recommendation string    `json:"recommendation"`
	CreatedAt      time.Time `json:"created_at"`
}

// ToolCall is a single tool invocation and its result.
type ToolCall struct {
	ID         string          `json:"id"`
	RunID      string          `json:"run_id"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	Result     string          `json:"result"`
	IsError    bool            `json:"is_error"`
	DurationMs int64           `json:"duration_ms"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Finding is a structured observation; nullable VulnID lets the agent
// record pre-triaged intel before promoting it to a vulnerability.
type Finding struct {
	ID            string    `json:"id"`
	RunID         string    `json:"run_id"`
	VulnID        *string   `json:"vuln_id,omitempty"`
	Category      string    `json:"category"`
	SeverityScore float64   `json:"severity_score"`
	Summary       string    `json:"summary"`
	CreatedAt     time.Time `json:"created_at"`
}

// Stores bundles every store the API handlers need.
type Stores struct {
	DB        *db.DB
	Targets   *TargetStore
	Runs      *RunStore
	Messages  *MessageStore
	Vulns     *VulnStore
	ToolCalls *ToolCallStore
	Findings  *FindingStore
}

// New constructs every store over the shared *db.DB.
func New(database *db.DB) *Stores {
	return &Stores{
		DB:        database,
		Targets:   &TargetStore{db: database},
		Runs:      &RunStore{db: database},
		Messages:  &MessageStore{db: database},
		Vulns:     &VulnStore{db: database},
		ToolCalls: &ToolCallStore{db: database},
		Findings:  &FindingStore{db: database},
	}
}

// ----- TargetStore -----

// TargetStore persists attack targets.
type TargetStore struct{ db *db.DB }

// Create inserts a new target. ID and CreatedAt are populated if zero.
func (s *TargetStore) Create(ctx context.Context, t *Target) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if len(t.Attributes) == 0 {
		t.Attributes = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO targets (id, type, value, normalized, attributes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.Type, t.Value, t.Normalized, string(t.Attributes), t.CreatedAt.UnixMilli())
	return err
}

// Get fetches a target by ID.
func (s *TargetStore) Get(ctx context.Context, id string) (*Target, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, type, value, normalized, attributes, created_at FROM targets WHERE id = ?`, id)
	return scanTarget(row)
}

// List returns the most-recently-created targets, newest first.
func (s *TargetStore) List(ctx context.Context, limit int) ([]*Target, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, value, normalized, attributes, created_at FROM targets
		 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTarget(r rowScanner) (*Target, error) {
	var t Target
	var attrs string
	var createdMs int64
	if err := r.Scan(&t.ID, &t.Type, &t.Value, &t.Normalized, &attrs, &createdMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.Attributes = json.RawMessage(attrs)
	t.CreatedAt = time.UnixMilli(createdMs).UTC()
	return &t, nil
}

// ----- RunStore -----

// RunStore persists agent runs.
type RunStore struct{ db *db.DB }

// Create inserts a new run.
func (s *RunStore) Create(ctx context.Context, r *Run) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	if r.Status == "" {
		r.Status = "running"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, target_id, objective, status, summary, started_at, ended_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.TargetID, r.Objective, r.Status, r.Summary,
		r.StartedAt.UnixMilli(), nullableMillis(r.EndedAt))
	return err
}

// Update mutates a run in place. Pass only the fields you want changed.
func (s *RunStore) Update(ctx context.Context, r *Run) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = ?, summary = ?, ended_at = ? WHERE id = ?`,
		r.Status, r.Summary, nullableMillis(r.EndedAt), r.ID)
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

// AddTokens adds token counts to an existing run. Zero values are
// ignored so partial updates are safe.
func (s *RunStore) AddTokens(ctx context.Context, runID string, in, out, cc, cr int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET
			input_tokens = input_tokens + ?,
			output_tokens = output_tokens + ?,
			cache_creation_input_tokens = cache_creation_input_tokens + ?,
			cache_read_input_tokens = cache_read_input_tokens + ?
		 WHERE id = ?`,
		in, out, cc, cr, runID)
	return err
}

// Get fetches a run by ID.
func (s *RunStore) Get(ctx context.Context, id string) (*Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, target_id, objective, status, summary, started_at, ended_at,
			input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens
		 FROM runs WHERE id = ?`, id)
	return scanRun(row)
}

// ListByTarget returns the most-recent runs for a target.
func (s *RunStore) ListByTarget(ctx context.Context, targetID string, limit int) ([]*Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, target_id, objective, status, summary, started_at, ended_at,
			input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens
		 FROM runs WHERE target_id = ? ORDER BY started_at DESC LIMIT ?`, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// List returns the most-recent runs, newest first.
func (s *RunStore) List(ctx context.Context, limit int) ([]*Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, target_id, objective, status, summary, started_at, ended_at,
			input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens
		 FROM runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRun(r rowScanner) (*Run, error) {
	var run Run
	var startedMs int64
	var endedMs sql.NullInt64
	if err := r.Scan(&run.ID, &run.TargetID, &run.Objective, &run.Status, &run.Summary, &startedMs, &endedMs,
		&run.InputTokens, &run.OutputTokens, &run.CacheCreationInputTokens, &run.CacheReadInputTokens); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	run.StartedAt = time.UnixMilli(startedMs).UTC()
	if endedMs.Valid {
		t := time.UnixMilli(endedMs.Int64).UTC()
		run.EndedAt = &t
	}
	return &run, nil
}

func nullableMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

// ----- MessageStore -----

// MessageStore persists streamed events.
type MessageStore struct{ db *db.DB }

// Append inserts a new message.
func (s *MessageStore) Append(ctx context.Context, m *Message) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if len(m.Payload) == 0 {
		m.Payload = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, run_id, role, event_type, content, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.RunID, m.Role, m.EventType, m.Content, string(m.Payload), m.CreatedAt.UnixMilli())
	return err
}

// ListByRun returns all messages for a run in insertion order.
func (s *MessageStore) ListByRun(ctx context.Context, runID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, role, event_type, content, payload, created_at
		 FROM messages WHERE run_id = ? ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanMessage(r rowScanner) (*Message, error) {
	var m Message
	var payload string
	var createdMs int64
	if err := r.Scan(&m.ID, &m.RunID, &m.Role, &m.EventType, &m.Content, &payload, &createdMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.Payload = json.RawMessage(payload)
	m.CreatedAt = time.UnixMilli(createdMs).UTC()
	return &m, nil
}

// ----- VulnStore -----

// VulnStore persists confirmed vulnerabilities.
type VulnStore struct{ db *db.DB }

// Create inserts a new vulnerability.
func (s *VulnStore) Create(ctx context.Context, v *Vulnerability) error {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.Status == "" {
		v.Status = "open"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vulnerabilities
		 (id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.RunID, v.TargetID, v.Title, v.Severity, v.Status, v.Target,
		v.Evidence, v.Impact, v.Recommendation, v.CreatedAt.UnixMilli())
	return err
}

// Get fetches a vulnerability by ID.
func (s *VulnStore) Get(ctx context.Context, id string) (*Vulnerability, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, created_at
		 FROM vulnerabilities WHERE id = ?`, id)
	return scanVuln(row)
}

// ListByRun returns vulnerabilities for a run.
func (s *VulnStore) ListByRun(ctx context.Context, runID string) ([]*Vulnerability, error) {
	return s.query(ctx,
		`SELECT id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, created_at
		 FROM vulnerabilities WHERE run_id = ? ORDER BY created_at ASC`, runID)
}

// ListAll returns every vulnerability, optionally filtered.
func (s *VulnStore) ListAll(ctx context.Context, runID, targetID, severity string) ([]*Vulnerability, error) {
	q := `SELECT id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, created_at
	      FROM vulnerabilities WHERE 1=1`
	args := []any{}
	if runID != "" {
		q += " AND run_id = ?"
		args = append(args, runID)
	}
	if targetID != "" {
		q += " AND target_id = ?"
		args = append(args, targetID)
	}
	if severity != "" {
		q += " AND severity = ?"
		args = append(args, severity)
	}
	q += " ORDER BY created_at DESC LIMIT 500"
	return s.query(ctx, q, args...)
}

func (s *VulnStore) query(ctx context.Context, q string, args ...any) ([]*Vulnerability, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Vulnerability
	for rows.Next() {
		v, err := scanVuln(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanVuln(r rowScanner) (*Vulnerability, error) {
	var v Vulnerability
	var createdMs int64
	if err := r.Scan(&v.ID, &v.RunID, &v.TargetID, &v.Title, &v.Severity, &v.Status,
		&v.Target, &v.Evidence, &v.Impact, &v.Recommendation, &createdMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	v.CreatedAt = time.UnixMilli(createdMs).UTC()
	return &v, nil
}

// ----- ToolCallStore -----

// ToolCallStore persists tool invocations.
type ToolCallStore struct{ db *db.DB }

// Append inserts a new tool call row.
func (s *ToolCallStore) Append(ctx context.Context, t *ToolCall) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if len(t.Arguments) == 0 {
		t.Arguments = json.RawMessage(`{}`)
	}
	isError := 0
	if t.IsError {
		isError = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tool_calls (id, run_id, name, arguments, result, is_error, duration_ms, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.RunID, t.Name, string(t.Arguments), t.Result, isError, t.DurationMs, t.CreatedAt.UnixMilli())
	return err
}

// ListByRun returns tool calls in insertion order.
func (s *ToolCallStore) ListByRun(ctx context.Context, runID string) ([]*ToolCall, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, name, arguments, result, is_error, duration_ms, created_at
		 FROM tool_calls WHERE run_id = ? ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ToolCall
	for rows.Next() {
		t, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanToolCall(r rowScanner) (*ToolCall, error) {
	var t ToolCall
	var args string
	var isError int
	var createdMs int64
	if err := r.Scan(&t.ID, &t.RunID, &t.Name, &args, &t.Result, &isError, &t.DurationMs, &createdMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.Arguments = json.RawMessage(args)
	t.IsError = isError != 0
	t.CreatedAt = time.UnixMilli(createdMs).UTC()
	return &t, nil
}

// ----- FindingStore -----

// FindingStore persists structured observations.
type FindingStore struct{ db *db.DB }

// Append is reserved for future use; not exercised by MVP handlers.
func (s *FindingStore) Append(ctx context.Context, f *Finding) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	var vulnID any
	if f.VulnID != nil {
		vulnID = *f.VulnID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO findings (id, run_id, vuln_id, category, severity_score, summary, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.RunID, vulnID, f.Category, f.SeverityScore, f.Summary, f.CreatedAt.UnixMilli())
	return err
}

// assertNotFound is a tiny helper used in tests / dev to coerce a wrapped
// error into ErrNotFound if the underlying cause was sql.ErrNoRows.
func assertNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("store: %w", err)
}
