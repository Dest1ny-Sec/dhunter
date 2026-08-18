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

	// messages_fts — full-text search over message content (trigram tokenizer
	// gives CJK + substring search). message_id is UNINDEXED and holds the
	// owning messages.id so hits can join back for context.
	`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		message_id UNINDEXED,
		content,
		tokenize = 'trigram'
	);`,
	// Backfill existing messages once (guarded: only when the FTS table is
	// empty). New inserts keep it in sync via MessageStore.Append.
	`INSERT INTO messages_fts (message_id, content)
	 SELECT id, content FROM messages
	 WHERE NOT EXISTS (SELECT 1 FROM messages_fts LIMIT 1);`,

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

	// settings — simple key/value store (LLM config, token budget, etc.)
	`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT ''
	);`,

	// assets — structured discovered assets (root-domain/subdomain/ip/
	// service/app/endpoint) with an optional parent for the asset tree.
	// Project-scoped (target_id); the discovering run_id is kept for audit.
	`CREATE TABLE IF NOT EXISTS assets (
		id          TEXT PRIMARY KEY,
		target_id   TEXT NOT NULL,
		run_id      TEXT NOT NULL DEFAULT '',
		type        TEXT NOT NULL,
		value       TEXT NOT NULL,
		meta        TEXT NOT NULL DEFAULT '',
		parent_id   TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_assets_target ON assets(target_id);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_target_value ON assets(target_id, value);`,

	// knowledge — cross-target reusable intel (endpoints, creds, fingerprints)
	// learned from one run and injected into later runs on the same host family.
	`CREATE TABLE IF NOT EXISTS knowledge (
		id          TEXT PRIMARY KEY,
		host_family TEXT NOT NULL,
		kind        TEXT NOT NULL,
		value       TEXT NOT NULL,
		created_at  INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_host ON knowledge(host_family);`,

	// mcp_servers — user-configured external MCP servers (the "extension
	// center"). The built-in dhunter-mcp is always on; rows here add
	// additional tool sources that the agent aggregates at run time.
	// `transport` is reserved for future SSE/stdio variants; v0.7.0 only
	// supports streamable-HTTP ("http"). `token` is stored as-is and
	// redacted in API responses (only returned on Create). `auth_header`
	// and `auth_scheme` let hosted MCPs use credentials such as a raw
	// X-API-Key instead of forcing Authorization: Bearer.
	`CREATE TABLE IF NOT EXISTS mcp_servers (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL UNIQUE,
		url         TEXT NOT NULL,
		transport   TEXT NOT NULL DEFAULT 'http',
		token       TEXT NOT NULL DEFAULT '',
		auth_header TEXT NOT NULL DEFAULT 'Authorization',
		auth_scheme TEXT NOT NULL DEFAULT 'Bearer',
		enabled     INTEGER NOT NULL DEFAULT 1,
		description TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		updated_at  INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled);`,

	// skills — user-installable agent skills (prompt + metadata). Built-in
	// skills are seeded into this table on first run; user-defined ones
	// come from the UI. The agent reads `enabled` rows into its system
	// prompt or tool instructions.
	//   source: 'builtin' (seeded) | 'community' (imported) | 'custom' (user-written)
	//   category: free-form (e.g. 'web', 'api', 'fuzz', 'reporting')
	`CREATE TABLE IF NOT EXISTS skills (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL UNIQUE,
		title       TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		content     TEXT NOT NULL DEFAULT '',
		category    TEXT NOT NULL DEFAULT 'general',
		tags        TEXT NOT NULL DEFAULT '',
		enabled     INTEGER NOT NULL DEFAULT 1,
		source      TEXT NOT NULL DEFAULT 'custom',
		created_at  INTEGER NOT NULL,
		updated_at  INTEGER NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_skills_enabled ON skills(enabled);`,
	`CREATE INDEX IF NOT EXISTS idx_skills_source ON skills(source);`,
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
		{"runs", "reasoning_tokens INTEGER NOT NULL DEFAULT 0"},
		{"vulnerabilities", "norm_title TEXT NOT NULL DEFAULT ''"},
		{"vulnerabilities", "reproduction TEXT NOT NULL DEFAULT ''"},
		{"vulnerabilities", "poc_evidence TEXT NOT NULL DEFAULT ''"},
		{"targets", "auth_context TEXT NOT NULL DEFAULT ''"},
		{"targets", "red_lines TEXT NOT NULL DEFAULT ''"},
		{"targets", "name TEXT NOT NULL DEFAULT ''"},
		{"targets", "favorite INTEGER NOT NULL DEFAULT 0"},
		{"targets", "authorization TEXT NOT NULL DEFAULT ''"},
		{"facts", "kind TEXT NOT NULL DEFAULT 'info'"},
		{"facts", "confidence REAL NOT NULL DEFAULT 0.5"},
		{"mcp_servers", "auth_header TEXT NOT NULL DEFAULT 'Authorization'"},
		{"mcp_servers", "auth_scheme TEXT NOT NULL DEFAULT 'Bearer'"},
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
