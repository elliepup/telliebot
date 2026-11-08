package bot

import (
	"strings"
	"testing"

	"github.com/elliepup/telliebot/internal/pick"
)

func TestKindOfID(t *testing.T) {
	cases := map[string]pick.Kind{"anilist:21": pick.Anime, "tmdb:movie:603": pick.Movie, "tmdb:tv:1399": pick.Show}
	for id, want := range cases {
		if got, ok := kindOfID(id); !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", id, got, ok, want)
		}
	}
	if _, ok := kindOfID("bogus:1"); ok {
		t.Error("unknown prefix should not match")
	}
}

func TestSimilarFilters(t *testing.T) {
	f := similarFilters(request{Rating: "8", Length: "short", Era: "2000s"}, "tmdb:movie:603")
	if f.MinRating != 8 {
		t.Errorf("rating should carry over: %v", f.MinRating)
	}
	if f.MovieMinutes.Max != 0 || f.Years.Min != 0 {
		t.Errorf("length and era should be dropped: %+v", f)
	}
	if !f.Exclude["tmdb:movie:603"] {
		t.Error("the source title must be excluded")
	}

	f = similarFilters(request{Rating: "8", From: fromWatchlist}, "anilist:1")
	if f.MinRating != pick.DefaultMinRating {
		t.Errorf("watchlist picks have no rating filter; want default, got %v", f.MinRating)
	}
}

func TestSimilarEmbedFooter(t *testing.T) {
	res := pick.Result{ID: "tmdb:movie:1", Kind: pick.Movie, Title: "Heat", Source: "TMDB"}
	e := similarEmbed(res, "Collateral", request{Mood: "cozy", Rating: "8", Length: "short"})
	got := e.Footer.Text
	if !strings.HasPrefix(got, "✨ Like Collateral · Movie") || !strings.Contains(got, "⭐ 8+") || !strings.HasSuffix(got, "via TMDB") {
		t.Errorf("footer: %q", got)
	}
	if strings.Contains(got, "Cozy") || strings.Contains(got, "Short") {
		t.Errorf("unused mood/length must not be claimed: %q", got)
	}
}
