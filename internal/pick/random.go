package pick

import (
	"context"
	"math/rand/v2"
)

// pageAttempts is how many random pages to try before falling back to page 1
// when pages turn out empty or fully excluded (e.g. everything seen).
const pageAttempts = 3

// RandomFromPages picks a random item from a random page of results.
//
// first is page 1, already fetched (it's needed to learn lastPage). fetch
// loads any other page. keep reports whether an item is usable; items it
// rejects are skipped. If a random page has nothing usable it tries another,
// then falls back to page 1, then returns ErrNoResults.
func RandomFromPages[T any](
	ctx context.Context,
	lastPage int,
	first []T,
	fetch func(ctx context.Context, page int) ([]T, error),
	keep func(T) bool,
) (T, error) {
	var zero T
	usable := func(items []T) []T {
		var out []T
		for _, it := range items {
			if keep(it) {
				out = append(out, it)
			}
		}
		return out
	}

	for attempt := 0; attempt < pageAttempts && lastPage > 1; attempt++ {
		p := rand.IntN(lastPage) + 1
		items := first
		if p != 1 {
			var err error
			if items, err = fetch(ctx, p); err != nil {
				return zero, err
			}
		}
		if ok := usable(items); len(ok) > 0 {
			return ok[rand.IntN(len(ok))], nil
		}
	}

	if ok := usable(first); len(ok) > 0 {
		return ok[rand.IntN(len(ok))], nil
	}
	return zero, ErrNoResults
}
