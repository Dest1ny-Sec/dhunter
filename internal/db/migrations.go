package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// migrations is the ordered list of schema statements. Dhunter is MVP, so
// we ship a single version and rely on `CREATE IF NOT EXISTS` to be
// idempotent across restarts. A future v0.2 can introduce a real
// versioned migrator without rewriting these.
var migrations = []string{
	// targets — what the user told us to attack
	`CREATE TABLE IF NOT EXISTS targets (
		id           TEXT PRIMARY KEY,
		type         TEXT NOT NULL,
		value        TEXT NOT NULL,
		normalized   TEXT NOT NULL,
		attributes   TEXT NOT NULL DEFAULT '{}',
		created_at   INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_targets_type ON targets(type);`,
	`CREATE INDEX IF NOT EXISTS idx_targets_normalized ON targets(normalized);`,

	// runs — one LLM-loop execution per row
	`CREATE TABLE IF NOT EXISTS runs (
		id          TEXT PRIMARY KEY,
		target_id   TEXT NOT NULL,
		objective   TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL,
		summary     TEXT NOT NULL DEFAULT '',
		started_at  INTEGER NOT NULL,
		ended_at    INTEGER
	);`,
	`CREATE INDEX IF NOT EXISTS idx_runs_target ON runs(target_id);`,
	`CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);`,

	// messages — every streamed message, plus tool/control messages
	`CREATE TABLE IF NOT EXISTS messages (
		id          TEXT PRIMARY KEY,
		run_id      TEXT NOT NULL,
		role        TEXT NOT NULL,
		event_type  TEXT NOT NULL DEFAULT '',
		content     TEXT NOT NULL DEFAULT '',
		payload     TEXT NOT NULL DEFAULT '{}',
		created_at  INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_messages_run ON messages(run_id);`,

	// vulnerabilities — confirmed issues, joined back to run + target
	`CREATE TABLE IF NOT EXISTS vulnerabilities (
		id              TEXT PRIMARY KEY,
		run_id          TEXT NOT NULL,
		target_id       TEXT NOT NULL,
		title           TEXT NOT NULL,
		severity        TEXT NOT NULL,
		status          TEXT NOT NULL DEFAULT 'open',
		target          TEXT NOT NULL DEFAULT '',
		evidence        TEXT NOT NULL DEFAULT '',
		impact          TEXT NOT NULL DEFAULT '',
		recommendation  TEXT NOT NULL DEFAULT '',
		created_at      INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_vuln_run ON vulnerabilities(run_id);`,
	`CREATE INDEX IF NOT EXISTS idx_vuln_target ON vulnerabilities(target_id);`,
	`CREATE INDEX IF NOT EXISTS idx_vuln_severity ON vulnerabilities(severity);`,

	// tool_calls — every tool invocation and its raw result
	`CREATE TABLE IF NOT EXISTS tool_calls (
		id          TEXT PRIMARY KEY,
		run_id      TEXT NOT NULL,
		name        TEXT NOT NULL,
		arguments   TEXT NOT NULL DEFAULT '{}',
		result      TEXT NOT NULL DEFAULT '',
		is_error    INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		created_at  INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_tool_calls_run ON tool_calls(run_id);`,

	// findings — structured observations that may or may not be vulns
	`CREATE TABLE IF NOT EXISTS findings (
		id              TEXT PRIMARY KEY,
		run_id          TEXT NOT NULL,
		vuln_id         TEXT,
		category        TEXT NOT NULL,
		severity_score  REAL NOT NULL DEFAULT 0,
		summary         TEXT NOT NULL DEFAULT '',
		created_at      INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_findings_run ON findings(run_id);`,

	// facts — the board's confirmed observations (cairn-style blackboard).
	// Facts are append-only: state changes are new facts, never edits.
	`CREATE TABLE IF NOT EXISTS facts (
		id          TEXT PRIMARY KEY,
		run_id      TEXT NOT NULL,
		description TEXT NOT NULL,
		source      TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_facts_run ON facts(run_id);`,

	// intents — declared directions of exploration on the board.
	//   open      → proposed, no worker claimed it yet
	//   claimed   → a worker is exploring it
	//   concluded → a worker produced a fact (to_fact_id set)
	//   failed    → the worker gave up / errored
	`CREATE TABLE IF NOT EXISTS intents (
		id           TEXT PRIMARY KEY,
		run_id       TEXT NOT NULL,
		from_facts   TEXT NOT NULL DEFAULT '[]',
		description  TEXT NOT NULL,
		creator      TEXT NOT NULL DEFAULT '',
		worker       TEXT,
		status       TEXT NOT NULL DEFAULT 'open',
		to_fact_id   TEXT,
		created_at   INTEGER NOT NULL,
		claimed_at   INTEGER,
		concluded_at INTEGER
	);`,
	`CREATE INDEX IF NOT EXISTS idx_intents_run ON intents(run_id);`,
	`CREATE INDEX IF NOT EXISTS idx_intents_status ON intents(status);`,

	// hints — human judgement injected into the board mid-run.
	`CREATE TABLE IF NOT EXISTS hints (
		id         TEXT PRIMARY KEY,
		run_id     TEXT NOT NULL,
		content    TEXT NOT NULL,
		creator    TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_hints_run ON hints(run_id);`,
}

// Migrate runs every DDL statement in order, then applies per-column
// additions that SQLite has no idempotent form for (ALTER TABLE ADD
// COLUMN fails if the column already exists).
func (d *DB) Migrate(ctx context.Context) error {
	for i, stmt := range migrations {
		if _, err := d.ExecContext(ctx, stmt); err != nil {
			return err
		}
		_ = i // index reserved for future migration logging
	}
	// Idempotent column adds — guarded by a probe so re-runs are safe.
	for _, col := range []struct{ table, def string }{
		{"runs", "input_tokens INTEGER NOT NULL DEFAULT 0"},
		{"runs", "output_tokens INTEGER NOT NULL DEFAULT 0"},
		{"runs", "cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0"},
		{"runs", "cache_read_input_tokens INTEGER NOT NULL DEFAULT 0"},
	} {
		if !d.columnExists(ctx, col.table, strings.TrimSpace(strings.SplitN(col.def, " ", 2)[0])) {
			if _, err := d.ExecContext(ctx, "ALTER TABLE "+col.table+" ADD COLUMN "+col.def); err != nil {
				return fmt.Errorf("add column %s.%s: %w", col.table, col.def, err)
			}
		}
	}
	return nil
}

// columnExists returns true when the named column is present on table.
func (d *DB) columnExists(ctx context.Context, table, column string) bool {
	row := d.QueryRowContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	for {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := row.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
		// SQL rows.Next isn't applicable to QueryRow; loop terminates
		// when Scan returns io.EOF on the next iteration. To iterate
		// we use QueryContext below.
		break
	}
	rows, err := d.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}
