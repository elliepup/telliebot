// Package storage persists seen titles and the watchlist in SQLite.
//
// Lists are keyed by a scope string: "guild:<id>" for a server (shared by
// everyone in it) or "user:<id>" for DMs. Titles are keyed by pick.Result.ID.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"

	"github.com/elliepup/telliebot/internal/pick"
)

// migrations run in order; the database's user_version records how many
// have been applied. Never edit an applied migration, only append.
var migrations = []string{
	// 1: items cache, seen list, watchlist.
	`CREATE TABLE items (
		key        TEXT PRIMARY KEY,
		kind       TEXT NOT NULL,
		title      TEXT NOT NULL,
		data       TEXT NOT NULL, -- pick.Result as JSON
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE seen (
		scope      TEXT NOT NULL,
		key        TEXT NOT NULL REFERENCES items(key),
		user_id    TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (scope, key)
	);
	CREATE TABLE watchlist (
		scope      TEXT NOT NULL,
		key        TEXT NOT NULL REFERENCES items(key),
		user_id    TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (scope, key)
	);
	CREATE INDEX watchlist_scope_created ON watchlist(scope, created_at DESC);`,
}

// Store is a SQLite-backed store. It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection avoids SQLITE_BUSY between our own goroutines; the
	// bot's write volume is tiny.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, now: time.Now}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		// PRAGMA doesn't take bound parameters; i is our own integer.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: set version: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: commit: %w", i+1, err)
		}
	}
	return nil
}

// SaveItem caches a picked title so the seen list and watchlist can refer to
// it by ID. Saving an existing ID refreshes its data.
func (s *Store) SaveItem(ctx context.Context, r pick.Result) error {
	if r.ID == "" {
		return errors.New("storage: result has no ID")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO items (key, kind, title, data, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET kind = excluded.kind, title = excluded.title,
			data = excluded.data, updated_at = excluded.updated_at`,
		r.ID, string(r.Kind), r.Title, string(data), s.now().Unix())
	return err
}

// Item returns a cached title.
func (s *Store) Item(ctx context.Context, key string) (pick.Result, bool, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM items WHERE key = ?`, key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return pick.Result{}, false, nil
	}
	if err != nil {
		return pick.Result{}, false, err
	}
	var r pick.Result
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return pick.Result{}, false, fmt.Errorf("decode item %s: %w", key, err)
	}
	return r, true, nil
}

// MarkSeen records that a title has been watched and removes it from the
// watchlist. It reports whether the title was on the watchlist, so an undo
// can put it back.
func (s *Store) MarkSeen(ctx context.Context, scope, key, userID string) (wasSaved bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM watchlist WHERE scope = ? AND key = ?`, scope, key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO seen (scope, key, user_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (scope, key) DO NOTHING`,
		scope, key, userID, s.now().Unix()); err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// UnmarkSeen removes a title from the seen list.
func (s *Store) UnmarkSeen(ctx context.Context, scope, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM seen WHERE scope = ? AND key = ?`, scope, key)
	return err
}

// SeenKeys returns every title ID marked as seen in a scope.
func (s *Store) SeenKeys(ctx context.Context, scope string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM seen WHERE scope = ?`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// AddToWatchlist saves a title to a scope's watchlist. Adding a title that's
// already there is a no-op.
func (s *Store) AddToWatchlist(ctx context.Context, scope, key, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO watchlist (scope, key, user_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (scope, key) DO NOTHING`,
		scope, key, userID, s.now().Unix())
	return err
}

// RemoveFromWatchlist removes a title from a scope's watchlist.
func (s *Store) RemoveFromWatchlist(ctx context.Context, scope, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM watchlist WHERE scope = ? AND key = ?`, scope, key)
	return err
}

// InWatchlist reports whether a title is on a scope's watchlist.
func (s *Store) InWatchlist(ctx context.Context, scope, key string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM watchlist WHERE scope = ? AND key = ?`, scope, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Entry is one watchlist row.
type Entry struct {
	Item    pick.Result
	AddedBy string
	AddedAt time.Time
}

// Watchlist returns the newest entries in a scope (up to limit), optionally
// filtered by kind ("" for all), and the total number matching.
func (s *Store) Watchlist(ctx context.Context, scope string, kind pick.Kind, limit int) ([]Entry, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM watchlist w JOIN items i ON i.key = w.key
		WHERE w.scope = ? AND (? = '' OR i.kind = ?)`,
		scope, string(kind), string(kind)).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT i.data, w.user_id, w.created_at FROM watchlist w JOIN items i ON i.key = w.key
		WHERE w.scope = ? AND (? = '' OR i.kind = ?)
		ORDER BY w.created_at DESC, w.rowid DESC LIMIT ?`,
		scope, string(kind), string(kind), limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var data, user string
		var created int64
		if err := rows.Scan(&data, &user, &created); err != nil {
			return nil, 0, err
		}
		var r pick.Result
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return nil, 0, err
		}
		out = append(out, Entry{Item: r, AddedBy: user, AddedAt: time.Unix(created, 0)})
	}
	return out, total, rows.Err()
}

// RandomFromWatchlist picks a random saved title in a scope, optionally
// filtered by kind ("" for all). ok is false when nothing matches.
func (s *Store) RandomFromWatchlist(ctx context.Context, scope string, kind pick.Kind) (r pick.Result, ok bool, err error) {
	var data string
	err = s.db.QueryRowContext(ctx, `
		SELECT i.data FROM watchlist w JOIN items i ON i.key = w.key
		WHERE w.scope = ? AND (? = '' OR i.kind = ?)
		ORDER BY random() LIMIT 1`,
		scope, string(kind), string(kind)).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return pick.Result{}, false, nil
	}
	if err != nil {
		return pick.Result{}, false, err
	}
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return pick.Result{}, false, err
	}
	return r, true, nil
}
