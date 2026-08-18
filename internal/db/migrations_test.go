package db

import (
	"context"
	"testing"
)

func TestMigrateAddsMCPAuthColumnsToExistingDatabase(t *testing.T) {
	d, err := Open(t.TempDir() + "/legacy.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = d.Close(context.Background()) }()

	_, err = d.ExecContext(context.Background(), `CREATE TABLE mcp_servers (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, url TEXT NOT NULL,
		transport TEXT NOT NULL DEFAULT 'http', token TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1, description TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	_, err = d.ExecContext(context.Background(), `INSERT INTO mcp_servers
		(id, name, url, transport, token, enabled, description, created_at, updated_at)
		VALUES ('1', 'legacy', 'https://example.test/mcp', 'http', 'tok', 1, '', 1, 1)`)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := d.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var header, scheme string
	if err := d.QueryRowContext(context.Background(),
		`SELECT auth_header, auth_scheme FROM mcp_servers WHERE id = '1'`,
	).Scan(&header, &scheme); err != nil {
		t.Fatalf("query migrated row: %v", err)
	}
	if header != "Authorization" || scheme != "Bearer" {
		t.Fatalf("unexpected defaults: header=%q scheme=%q", header, scheme)
	}
}
