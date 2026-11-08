package tmdb

import (
	"testing"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

func TestQueryMovieFilters(t *testing.T) {
	f := pick.ParseFilters("8", "short", "2000s")
	q := query(pick.Movie, mood.Mood{MovieGenres: []int{35, 18}}, f)

	want := map[string]string{
		"vote_average.gte":         "8",
		"with_genres":              "35|18",
		"with_runtime.lte":         "100",
		"primary_release_date.gte": "2000-01-01",
		"primary_release_date.lte": "2009-12-31",
	}
	for k, w := range want {
		if got := q.Get(k); got != w {
			t.Errorf("%s: got %q, want %q", k, got, w)
		}
	}
	if q.Has("with_runtime.gte") {
		t.Errorf("short should have no runtime minimum")
	}
}

func TestQueryShowUsesEpisodeRuntimeAndAirDate(t *testing.T) {
	f := pick.ParseFilters("", "long", "classic")
	q := query(pick.Show, mood.Mood{ShowGenres: []int{18}}, f)

	if got := q.Get("with_runtime.gte"); got != "51" {
		t.Errorf("with_runtime.gte: got %q, want 51", got)
	}
	if got := q.Get("first_air_date.lte"); got != "1999-12-31" {
		t.Errorf("first_air_date.lte: got %q", got)
	}
	if q.Has("primary_release_date.lte") {
		t.Errorf("shows must not use movie date params")
	}
	if got := q.Get("vote_average.gte"); got != "6.5" {
		t.Errorf("default rating: got %q, want 6.5", got)
	}
}
