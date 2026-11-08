package pick

import (
	"context"
	"errors"
	"testing"
)

func pages(n, per int) map[int][]int {
	out := map[int][]int{}
	id := 0
	for p := 1; p <= n; p++ {
		for i := 0; i < per; i++ {
			id++
			out[p] = append(out[p], id)
		}
	}
	return out
}

func TestRandomFromPagesSkipsRejected(t *testing.T) {
	ps := pages(5, 4)
	fetch := func(_ context.Context, p int) ([]int, error) { return ps[p], nil }
	// Reject everything except item 2 (on page 1).
	keep := func(id int) bool { return id == 2 }

	for i := 0; i < 50; i++ {
		got, err := RandomFromPages(context.Background(), 5, ps[1], fetch, keep)
		if err != nil || got != 2 {
			t.Fatalf("got %d, %v; want 2", got, err)
		}
	}
}

func TestRandomFromPagesFindsOtherPagesWhenFirstExcluded(t *testing.T) {
	// Two pages: page 1 is all seen, page 2 has one usable item. With 3
	// attempts over 2 pages, the chance of never drawing page 2 is 1/8, so
	// run enough times that a broken implementation would show up.
	ps := pages(2, 3)
	fetch := func(_ context.Context, p int) ([]int, error) { return ps[p], nil }
	keep := func(id int) bool { return id == 5 }
	hits := 0
	for i := 0; i < 200; i++ {
		if got, err := RandomFromPages(context.Background(), 2, ps[1], fetch, keep); err == nil && got == 5 {
			hits++
		}
	}
	if hits < 150 {
		t.Fatalf("found the page-2 item only %d/200 times", hits)
	}
}

func TestRandomFromPagesAllRejected(t *testing.T) {
	ps := pages(3, 2)
	fetch := func(_ context.Context, p int) ([]int, error) { return ps[p], nil }
	_, err := RandomFromPages(context.Background(), 3, ps[1], fetch, func(int) bool { return false })
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("got %v, want ErrNoResults", err)
	}
}

func TestRandomFromPagesEmptyPageFallsBack(t *testing.T) {
	// lastPage claims 10 pages, but only page 1 has items (AniList's
	// lastPage can overstate).
	first := []int{7, 8}
	fetch := func(context.Context, int) ([]int, error) { return nil, nil }
	got, err := RandomFromPages(context.Background(), 10, first, fetch, func(int) bool { return true })
	if err != nil || (got != 7 && got != 8) {
		t.Fatalf("got %d, %v", got, err)
	}
}

func TestRandomFromPagesFetchError(t *testing.T) {
	boom := errors.New("boom")
	fetch := func(context.Context, int) ([]int, error) { return nil, boom }
	// With 1000 pages, a random page other than 1 is near-certain to be tried.
	_, err := RandomFromPages(context.Background(), 1000, []int{1}, fetch, func(int) bool { return true })
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
}
