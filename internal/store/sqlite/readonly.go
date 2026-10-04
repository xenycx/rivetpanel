package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"time"
)

// OpenReadOnly opens an existing database without creating, migrating or
// changing it (for `rivetpanel doctor`). It fails when the file is missing.
func OpenReadOnly(ctx context.Context, path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	// A stopped panel leaves no write-ahead log; immutable then keeps SQLite
	// from creating -wal/-shm files. A running panel's log is read normally.
	if _, err := os.Stat(path + "-wal"); os.IsNotExist(err) {
		q.Set("immutable", "1")
	}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	dsn := (&url.URL{Scheme: "file", Opaque: escapePath(path), RawQuery: q.Encode()}).String()
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(1)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sdb.PingContext(pctx); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("open database read-only: %w", err)
	}
	return &DB{sdb}, nil
}
