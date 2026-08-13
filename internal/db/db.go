// Package db owns the SQLite connection used by every store.
//
// We use modernc.org/sqlite so the binary stays pure Go (no CGO, no
// sqlite3.dylib) and the same binary runs on macOS, Linux and Windows
// without a build matrix.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB so we can hang helpers (Close with timeout, tx factory,
// etc.) on it without leaking the stdlib type through the rest of the app.
type DB struct {
	*sql.DB
}

// Open opens (or creates) the SQLite file at path, enables foreign keys
// and WAL mode, and ensures the parent directory exists.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := mkdirAll(dir); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	// _pragma values: enable FK enforcement, switch to WAL for
	// concurrent reads, and use NORMAL sync for a sane speed/durability
	// trade-off on local disk.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite is happiest with a single writer; let the driver manage
	// the pool. The Go driver is fine with multiple readers, so we keep
	// MaxOpenConns at 1 to avoid "database is locked" pain during
	// migration while still allowing some concurrency via SetMaxIdleConns.
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := raw.PingContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return &DB{DB: raw}, nil
}

// Close closes the underlying *sql.DB. The context argument is reserved
// for future use (draining writes) but currently just delegates.
func (d *DB) Close(_ context.Context) error {
	if d == nil || d.DB == nil {
		return nil
	}
	return d.DB.Close()
}

// mkdirAll is a tiny wrapper so the import surface stays compact.
func mkdirAll(dir string) error {
	return mkdirAllOS(dir)
}
