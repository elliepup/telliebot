package tmdb

import (
	"strings"
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
	if got := q.Get("without_genres"); got != showGenresExcluded {
		t.Errorf("without_genres: got %q, want %q", got, showGenresExcluded)
	}
}

func TestQueryMovieKeepsAllGenres(t *testing.T) {
	q := query(pick.Movie, mood.Surprise, pick.ParseFilters("", "", ""))
	if q.Has("without_genres") {
		t.Errorf("movies should not exclude genres, got %q", q.Get("without_genres"))
	}
}

func TestQueryShowExcludesKids(t *testing.T) {
	q := query(pick.Show, mood.Surprise, pick.ParseFilters("", "", ""))
	if !strings.Contains(q.Get("without_genres"), "10762") {
		t.Errorf("shows should exclude Kids (10762), got %q", q.Get("without_genres"))
	}
}

func TestIsAnime(t *testing.T) {
	cases := []struct {
		name string
		r    result
		want bool
	}{
		{"japanese animation", result{OriginalLanguage: "ja", GenreIDs: []int{16, 10759}}, true},
		{"japanese live action", result{OriginalLanguage: "ja", GenreIDs: []int{18}}, false},
		{"western animation", result{OriginalLanguage: "en", GenreIDs: []int{16, 35}}, false},
		{"chinese animation", result{OriginalLanguage: "zh", GenreIDs: []int{16}}, false},
		{"no genres", result{OriginalLanguage: "ja"}, false},
	}
	for _, c := range cases {
		if got := isAnime(c.r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
