package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elliepup/telliebot/internal/pick"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var (
	ctx     = context.Background()
	scope   = "guild:1"
	other   = "guild:2"
	frieren = pick.Result{ID: "anilist:154587", Kind: pick.Anime, Title: "Frieren", Fields: []pick.Field{{Name: "Score", Value: "9.1/10"}}}
	matrix  = pick.Result{ID: "tmdb:movie:603", Kind: pick.Movie, Title: "The Matrix", TrailerURL: "https://www.youtube.com/watch?v=x"}
)

func mustSave(t *testing.T, s *Store, rs ...pick.Result) {
	t.Helper()
	for _, r := range rs {
		if err := s.SaveItem(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReopenKeepsDataAndSkipsMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustSave(t, s, frieren)
	if err := s.AddToWatchlist(ctx, scope, frieren.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path) // would fail with "table already exists" if migrations re-ran
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ok, _ := s.InWatchlist(ctx, scope, frieren.ID); !ok {
		t.Fatal("watchlist entry lost after reopen")
	}
}

func TestItemRoundTrip(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, matrix)
	got, ok, err := s.Item(ctx, matrix.ID)
	if err != nil || !ok || got.Title != matrix.Title || got.TrailerURL != matrix.TrailerURL {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := s.Item(ctx, "nope"); ok {
		t.Fatal("missing item reported as found")
	}

	// Upsert refreshes data.
	updated := matrix
	updated.Title = "The Matrix (1999)"
	mustSave(t, s, updated)
	if got, _, _ := s.Item(ctx, matrix.ID); got.Title != updated.Title {
		t.Fatalf("upsert: got %q", got.Title)
	}
}

func TestSaveItemRequiresID(t *testing.T) {
	if err := newStore(t).SaveItem(ctx, pick.Result{Title: "x"}); err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestWatchlistAddIsIdempotentAndScoped(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, frieren, matrix)
	for i := 0; i < 2; i++ {
		if err := s.AddToWatchlist(ctx, scope, frieren.ID, "u1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, total, _ := s.Watchlist(ctx, scope, "", 25); total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	if ok, _ := s.InWatchlist(ctx, other, frieren.ID); ok {
		t.Fatal("watchlist leaked across scopes")
	}
}

func TestWatchlistOrderKindAndLimit(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, frieren, matrix)
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	s.AddToWatchlist(ctx, scope, frieren.ID, "u1")
	clock = clock.Add(time.Minute)
	s.AddToWatchlist(ctx, scope, matrix.ID, "u2")

	es, total, err := s.Watchlist(ctx, scope, "", 1)
	if err != nil || total != 2 || len(es) != 1 || es[0].Item.ID != matrix.ID || es[0].AddedBy != "u2" {
		t.Fatalf("newest-first with limit: got %+v total=%d err=%v", es, total, err)
	}
	es, total, _ = s.Watchlist(ctx, scope, pick.Anime, 25)
	if total != 1 || len(es) != 1 || es[0].Item.ID != frieren.ID {
		t.Fatalf("kind filter: got %+v total=%d", es, total)
	}
	if es[0].Item.Fields[0].Value != "9.1/10" {
		t.Fatalf("fields not preserved: %+v", es[0].Item.Fields)
	}
}

func TestMarkSeenRemovesFromWatchlistAndUndo(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, frieren, matrix)
	s.AddToWatchlist(ctx, scope, frieren.ID, "u1")

	was, err := s.MarkSeen(ctx, scope, frieren.ID, "u2")
	if err != nil || !was {
		t.Fatalf("MarkSeen on saved title: was=%v err=%v", was, err)
	}
	if ok, _ := s.InWatchlist(ctx, scope, frieren.ID); ok {
		t.Fatal("seen title still on watchlist")
	}
	if was, _ := s.MarkSeen(ctx, scope, matrix.ID, "u2"); was {
		t.Fatal("unsaved title reported as saved")
	}
	// Marking twice is fine.
	if _, err := s.MarkSeen(ctx, scope, matrix.ID, "u1"); err != nil {
		t.Fatal(err)
	}

	seen, _ := s.SeenKeys(ctx, scope)
	if len(seen) != 2 || !seen[frieren.ID] || !seen[matrix.ID] {
		t.Fatalf("seen: got %v", seen)
	}
	if other, _ := s.SeenKeys(ctx, other); len(other) != 0 {
		t.Fatalf("seen leaked across scopes: %v", other)
	}

	if err := s.UnmarkSeen(ctx, scope, frieren.ID); err != nil {
		t.Fatal(err)
	}
	if seen, _ := s.SeenKeys(ctx, scope); seen[frieren.ID] {
		t.Fatal("unmark did not remove")
	}
}

func TestSeenRequiresKnownItem(t *testing.T) {
	// Foreign keys are on, so a seen row can't point at an uncached title.
	if _, err := newStore(t).MarkSeen(ctx, scope, "anilist:999", "u1"); err == nil {
		t.Fatal("expected foreign key error")
	}
}

func TestRandomFromWatchlist(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, frieren, matrix)
	if _, ok, err := s.RandomFromWatchlist(ctx, scope, ""); ok || err != nil {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
	s.AddToWatchlist(ctx, scope, frieren.ID, "u1")
	s.AddToWatchlist(ctx, scope, matrix.ID, "u1")

	for i := 0; i < 20; i++ {
		r, ok, err := s.RandomFromWatchlist(ctx, scope, pick.Movie)
		if err != nil || !ok || r.ID != matrix.ID {
			t.Fatalf("kind filter: got %+v ok=%v err=%v", r, ok, err)
		}
	}
	if _, ok, _ := s.RandomFromWatchlist(ctx, scope, pick.Show); ok {
		t.Fatal("no shows saved, but got one")
	}
}

func TestRandomFromWatchlistN(t *testing.T) {
	s := newStore(t)
	mustSave(t, s, frieren, matrix)
	s.AddToWatchlist(ctx, scope, frieren.ID, "u1")
	s.AddToWatchlist(ctx, scope, matrix.ID, "u1")

	rs, err := s.RandomFromWatchlistN(ctx, scope, "", 5)
	if err != nil || len(rs) != 2 || rs[0].ID == rs[1].ID {
		t.Fatalf("want 2 distinct, got %+v err=%v", rs, err)
	}
	if rs, _ := s.RandomFromWatchlistN(ctx, other, "", 5); len(rs) != 0 {
		t.Fatalf("other scope should be empty, got %d", len(rs))
	}
}
