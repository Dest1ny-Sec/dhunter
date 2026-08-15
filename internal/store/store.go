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
	"strings"
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
	// AuthContext holds an authenticated session (cookies + headers) the
	// agent auto-injects when testing this target — e.g. a white-hat's
	// logged-in session so the agent can test behind-auth function points.
	AuthContext string    `json:"auth_context"`
	// RedLines are custom user rules/guardrails the agent MUST always follow
	// (e.g. "禁止爆破", "只在授权范围", "不测支付接口"). Newline-separated.
	RedLines  string    `json:"red_lines,omitempty"`
	// Name is an optional human-friendly project name (falls back to Value).
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
	ReasoningTokens          int `json:"reasoning_tokens"`
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
	// Reproduction holds step-by-step repro instructions (curl + expected
	// result) so a report is actionable without re-deriving the PoC.
	Reproduction string    `json:"reproduction,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	// NormTitle is a dedup key (lowercased, trimmed, whitespace-collapsed).
	// It is computed by the store and never exposed over the API.
	NormTitle string `json:"-"`
}

// normalizeTitle reduces a finding title to a stable dedup key so that
// near-identical titles written by different workers (or LLM turns) are
// treated as the same finding.
func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// collapse runs of whitespace
	s = strings.Join(strings.Fields(s), " ")
	// strip surrounding punctuation / trailing period
	s = strings.Trim(s, ".,;:!?()[]{}'\"` ")
	return s
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
	Board     *Board
	Settings  *SettingsStore
	Knowledge *KnowledgeStore
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
		Board:     newBoard(database),
		Settings:  &SettingsStore{db: database},
		Knowledge: &KnowledgeStore{db: database},
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
		`INSERT INTO targets (id, type, value, normalized, attributes, auth_context, red_lines, name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Type, t.Value, t.Normalized, string(t.Attributes), t.AuthContext, t.RedLines, t.Name, t.CreatedAt.UnixMilli())
	return err
}

// SetAuth stores (or clears) the authenticated session for a target.
func (s *TargetStore) SetAuth(ctx context.Context, id, auth string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE targets SET auth_context = ? WHERE id = ?`, auth, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRedLines stores (or clears) the custom guardrails for a target.
func (s *TargetStore) SetRedLines(ctx context.Context, id, redLines string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE targets SET red_lines = ? WHERE id = ?`, redLines, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a target and everything tied to it (runs, vulns, board).
func (s *TargetStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// find runs for cascade
	rows, err := tx.QueryContext(ctx, `SELECT id FROM runs WHERE target_id = ?`, id)
	if err != nil {
		return err
	}
	var runIDs []string
	for rows.Next() {
		var rid string
		if err := rows.Scan(&rid); err != nil {
			rows.Close()
			return err
		}
		runIDs = append(runIDs, rid)
	}
	rows.Close()
	for _, rid := range runIDs {
		for _, q := range []string{
			`DELETE FROM facts WHERE run_id = ?`,
			`DELETE FROM intents WHERE run_id = ?`,
			`DELETE FROM hints WHERE run_id = ?`,
			`DELETE FROM messages WHERE run_id = ?`,
			`DELETE FROM tool_calls WHERE run_id = ?`,
			`DELETE FROM vulnerabilities WHERE run_id = ?`,
			`DELETE FROM findings WHERE run_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, rid); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE target_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM targets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// Get fetches a target by ID.
func (s *TargetStore) Get(ctx context.Context, id string) (*Target, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, type, value, normalized, attributes, auth_context, red_lines, name, created_at FROM targets WHERE id = ?`, id)
	return scanTarget(row)
}

// List returns the most-recently-created targets, newest first.
func (s *TargetStore) List(ctx context.Context, limit int) ([]*Target, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, value, normalized, attributes, auth_context, red_lines, name, created_at FROM targets
		 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Target, 0)
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
	if err := r.Scan(&t.ID, &t.Type, &t.Value, &t.Normalized, &attrs, &t.AuthContext, &t.RedLines, &t.Name, &createdMs); err != nil {
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

// RecoverStale marks any run still stuck in running/pending as failed.
// Called at server startup: if the process died mid-run, those runs have
// no live agent session and would otherwise be "running" forever.
func (s *RunStore) RecoverStale(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status = 'failed', summary = 'interrupted by server restart' WHERE status IN ('running','pending')`)
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
func (s *RunStore) AddTokens(ctx context.Context, runID string, in, out, cc, cr, re int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET
			input_tokens = input_tokens + ?,
			output_tokens = output_tokens + ?,
			cache_creation_input_tokens = cache_creation_input_tokens + ?,
			cache_read_input_tokens = cache_read_input_tokens + ?,
			reasoning_tokens = reasoning_tokens + ?
		 WHERE id = ?`,
		in, out, cc, cr, re, runID)
	return err
}

// Get fetches a run by ID.
func (s *RunStore) Get(ctx context.Context, id string) (*Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, target_id, objective, status, summary, started_at, ended_at,
			input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
			reasoning_tokens
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
			input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
			reasoning_tokens
		 FROM runs WHERE target_id = ? ORDER BY started_at DESC LIMIT ?`, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Run, 0)
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
			input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
			reasoning_tokens
		 FROM runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Run, 0)
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
		&run.InputTokens, &run.OutputTokens, &run.CacheCreationInputTokens, &run.CacheReadInputTokens,
		&run.ReasoningTokens); err != nil {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (id, run_id, role, event_type, content, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.RunID, m.Role, m.EventType, m.Content, string(m.Payload), m.CreatedAt.UnixMilli()); err != nil {
		return err
	}
	// Keep the FTS index in sync (best-effort: search availability must not
	// break message persistence).
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages_fts (message_id, content) VALUES (?, ?)`,
		m.ID, m.Content); err != nil {
		return err
	}
	return tx.Commit()
}

// MessageHit is one full-text search result, joined back to its run + target.
type MessageHit struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Role      string    `json:"role"`
	EventType string    `json:"event_type"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	TargetID  string    `json:"target_id,omitempty"`
	Target    string    `json:"target,omitempty"`
}

// Search full-text searches message content (trigram FTS for queries of 3+
// chars, LIKE fallback for shorter ones). Results are newest first.
func (s *MessageStore) Search(ctx context.Context, query string, limit int) ([]*MessageHit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return []*MessageHit{}, nil
	}
	var rows *sql.Rows
	var err error
	if len([]rune(q)) >= 3 {
		// trigram MATCH: the tokenizer requires >= 3 characters. Wrap the
		// user input in an FTS5 phrase literal (doubling embedded quotes) so
		// FTS5 syntax like `AND`, `OR`, `'` or `"` is treated as literal text
		// instead of a query operator — otherwise those queries 500.
		escaped := strings.ReplaceAll(q, `"`, `""`)
		rows, err = s.db.QueryContext(ctx,
			`SELECT m.id, m.run_id, m.role, m.event_type, m.content, m.created_at,
			        r.target_id, t.normalized
			 FROM messages m
			 JOIN runs r ON r.id = m.run_id
			 LEFT JOIN targets t ON t.id = r.target_id
			 WHERE m.id IN (SELECT message_id FROM messages_fts WHERE messages_fts MATCH '"' || ? || '"')
			 ORDER BY m.created_at DESC LIMIT ?`, escaped, limit)
	} else {
		like := "%" + q + "%"
		rows, err = s.db.QueryContext(ctx,
			`SELECT m.id, m.run_id, m.role, m.event_type, m.content, m.created_at,
			        r.target_id, t.normalized
			 FROM messages m
			 JOIN runs r ON r.id = m.run_id
			 LEFT JOIN targets t ON t.id = r.target_id
			 WHERE m.content LIKE ? OR m.payload LIKE ?
			 ORDER BY m.created_at DESC LIMIT ?`, like, like, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*MessageHit, 0)
	for rows.Next() {
		var h MessageHit
		var targetID sql.NullString
		var target sql.NullString
		var ts int64
		if err := rows.Scan(&h.ID, &h.RunID, &h.Role, &h.EventType, &h.Content, &ts,
			&targetID, &target); err != nil {
			return nil, err
		}
		h.CreatedAt = time.UnixMilli(ts).UTC()
		if targetID.Valid {
			h.TargetID = targetID.String
		}
		if target.Valid {
			h.Target = target.String
		}
		out = append(out, &h)
	}
	return out, rows.Err()
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
	out := make([]*Message, 0)
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
	if v.NormTitle == "" {
		v.NormTitle = normalizeTitle(v.Title)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vulnerabilities
		 (id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, reproduction, created_at, norm_title)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.RunID, v.TargetID, v.Title, v.Severity, v.Status, v.Target,
		v.Evidence, v.Impact, v.Recommendation, v.Reproduction, v.CreatedAt.UnixMilli(), v.NormTitle)
	return err
}

// UpdateStatus changes a vulnerability's lifecycle status. Used by the
// verifier to flip pending -> confirmed / dismissed.
func (s *VulnStore) UpdateStatus(ctx context.Context, id, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE vulnerabilities SET status = ? WHERE id = ?`, status, id)
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

// UpdateSeverity corrects a vulnerability's severity (verifier calibration).
func (s *VulnStore) UpdateSeverity(ctx context.Context, id, severity string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE vulnerabilities SET severity = ? WHERE id = ?`, severity, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FindDuplicate returns the ID of an existing vuln in the same run with
// the same (normalized) title + target, or "" when none exists.
func (s *VulnStore) FindDuplicate(ctx context.Context, runID, title, target string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM vulnerabilities WHERE run_id = ? AND title = ? AND target = ? LIMIT 1`,
		runID, title, target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// CreateIfAbsent inserts a vulnerability only if no duplicate
// (run_id + title + target) exists. The check and insert happen in one
// transaction, so concurrent workers submitting the same finding can't
// both pass the duplicate check (which is what made FindDuplicate + Create
// racy). Returns created=true when inserted, otherwise the existing ID.
func (s *VulnStore) CreateIfAbsent(ctx context.Context, v *Vulnerability) (created bool, existingID string, err error) {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.Status == "" {
		v.Status = "pending"
	}
	if v.NormTitle == "" {
		v.NormTitle = normalizeTitle(v.Title)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()

	var id string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM vulnerabilities WHERE run_id = ? AND norm_title = ? AND target = ? LIMIT 1`,
		v.RunID, v.NormTitle, v.Target).Scan(&id)
	if err == nil {
		return false, id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO vulnerabilities
		 (id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, reproduction, created_at, norm_title)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.RunID, v.TargetID, v.Title, v.Severity, v.Status, v.Target,
		v.Evidence, v.Impact, v.Recommendation, v.Reproduction, v.CreatedAt.UnixMilli(), v.NormTitle)
	if err != nil {
		return false, "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, "", errors.New("insert vulnerability affected 0 rows")
	}
	if err := tx.Commit(); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// Get fetches a vulnerability by ID.
func (s *VulnStore) Get(ctx context.Context, id string) (*Vulnerability, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, reproduction, created_at
		 FROM vulnerabilities WHERE id = ?`, id)
	return scanVuln(row)
}

// ListByRun returns vulnerabilities for a run.
func (s *VulnStore) ListByRun(ctx context.Context, runID string) ([]*Vulnerability, error) {
	return s.query(ctx,
		`SELECT id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, reproduction, created_at
		 FROM vulnerabilities WHERE run_id = ? ORDER BY created_at ASC`, runID)
}

// ListAll returns every vulnerability, optionally filtered.
func (s *VulnStore) ListAll(ctx context.Context, runID, targetID, severity string) ([]*Vulnerability, error) {
	q := `SELECT id, run_id, target_id, title, severity, status, target, evidence, impact, recommendation, reproduction, created_at
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
	out := make([]*Vulnerability, 0)
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
		&v.Target, &v.Evidence, &v.Impact, &v.Recommendation, &v.Reproduction, &createdMs); err != nil {
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

// AppendResult fills the result into the latest tool_call row for the same
// run+name that has no result yet — so a single logical tool invocation maps
// to ONE row (call args + result), not two. Falls back to a standalone row if
// no matching open call exists.
func (s *ToolCallStore) AppendResult(ctx context.Context, runID, name, result string, isError bool, durationMs int64) error {
	ie := 0
	if isError {
		ie = 1
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE tool_calls SET result = ?, is_error = ?, duration_ms = ?
		 WHERE id = (SELECT id FROM tool_calls
		             WHERE run_id = ? AND name = ? AND result = ''
		             ORDER BY created_at DESC, id DESC LIMIT 1)`,
		result, ie, durationMs, runID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// No open row to merge into — persist the result as its own record.
	return s.Append(ctx, &ToolCall{
		RunID: runID, Name: name, Result: result,
		IsError: isError, DurationMs: durationMs, CreatedAt: time.Now().UTC(),
	})
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
	out := make([]*ToolCall, 0)
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

// ----- KnowledgeStore -----

// Knowledge is a reusable intel item learned from a run.
type Knowledge struct {
	ID          string    `json:"id"`
	HostFamily  string    `json:"host_family"`
	Kind        string    `json:"kind"` // endpoint | credential | fingerprint | behavior
	Value       string    `json:"value"`
	CreatedAt   time.Time `json:"created_at"`
}

// KnowledgeStore persists cross-target intel.
type KnowledgeStore struct{ db *db.DB }

func (s *KnowledgeStore) Add(ctx context.Context, k *Knowledge) error {
	// dedup: same host_family + kind + value is already known
	var existing string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM knowledge WHERE host_family = ? AND kind = ? AND value = ? LIMIT 1`,
		k.HostFamily, k.Kind, k.Value).Scan(&existing)
	if err == nil {
		return nil // already have it
	}
	if k.ID == "" {
		k.ID = uuid.NewString()
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO knowledge (id, host_family, kind, value, created_at) VALUES (?, ?, ?, ?, ?)`,
		k.ID, k.HostFamily, k.Kind, k.Value, k.CreatedAt.UnixMilli())
	return err
}

func (s *KnowledgeStore) ListByFamily(ctx context.Context, hostFamily string, limit int) ([]*Knowledge, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, host_family, kind, value, created_at FROM knowledge
		 WHERE host_family = ? ORDER BY created_at DESC LIMIT ?`, hostFamily, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Knowledge, 0)
	for rows.Next() {
		var k Knowledge
		var ms int64
		if err := rows.Scan(&k.ID, &k.HostFamily, &k.Kind, &k.Value, &ms); err != nil {
			return nil, err
		}
		k.CreatedAt = time.UnixMilli(ms).UTC()
		out = append(out, &k)
	}
	return out, rows.Err()
}

// ----- SettingsStore -----

// SettingsStore is a simple key/value store for platform settings
// (LLM config, token budget, etc.).
type SettingsStore struct{ db *db.DB }

// Get returns a setting value ("" if unset).
func (s *SettingsStore) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// Set stores a setting value (upsert).
func (s *SettingsStore) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
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

// ClearAllData wipes all test/scan data — targets, runs, their messages and
// tool calls, the board (facts/intents/hints), vulnerabilities, and reusable
// knowledge — while preserving settings (admin credentials, LLM config,
// budget). Called from the "清空数据" action in the UI.
func (s *Stores) ClearAllData(ctx context.Context) error {
	stmts := []string{
		`DELETE FROM messages;`,
		`DELETE FROM tool_calls;`,
		`DELETE FROM facts;`,
		`DELETE FROM intents;`,
		`DELETE FROM hints;`,
		`DELETE FROM vulnerabilities;`,
		`DELETE FROM runs;`,
		`DELETE FROM targets;`,
		`DELETE FROM knowledge;`,
		`DELETE FROM messages_fts;`,
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
