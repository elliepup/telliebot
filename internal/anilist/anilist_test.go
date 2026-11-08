package anilist

import (
	"testing"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

func TestVariablesConvertsInclusiveToExclusive(t *testing.T) {
	f := pick.Filters{
		MinRating:     7.5,
		AnimeEpisodes: pick.Range{Min: 14, Max: 26},
		Years:         pick.Range{Min: 2010, Max: 2019},
	}
	v := variables(mood.Mood{AnimeGenres: []string{"Drama"}}, f)

	want := map[string]int{
		"minScore":     74,       // > 74  == >= 75
		"epGreater":    13,       // > 13  == >= 14
		"epLesser":     27,       // < 27  == <= 26
		"startGreater": 20099999, // > 2009-99-99 == from 2010-00-00
		"startLesser":  20200000, // < 2020-00-00 == through 2019
	}
	for k, w := range want {
		if got, ok := v[k].(int); !ok || got != w {
			t.Errorf("%s: got %v, want %d", k, v[k], w)
		}
	}
}

func TestVariablesExcludesSeenAniListIDsOnly(t *testing.T) {
	f := pick.ParseFilters("", "", "")
	f.Exclude = map[string]bool{"anilist:21": true, "anilist:1535": true, "tmdb:movie:603": true, "anilist:bad": true}
	ids, ok := variables(mood.Surprise, f)["idNotIn"].([]int)
	if !ok || len(ids) != 2 {
		t.Fatalf("idNotIn: got %v", ids)
	}
	got := map[int]bool{ids[0]: true, ids[1]: true}
	if !got[21] || !got[1535] {
		t.Fatalf("idNotIn: got %v, want 21 and 1535", ids)
	}
}

func TestVariablesOmitsUnsetFilters(t *testing.T) {
	v := variables(mood.Surprise, pick.ParseFilters("", "", ""))
	for _, k := range []string{"genres", "epGreater", "epLesser", "startGreater", "startLesser", "idNotIn"} {
		if _, ok := v[k]; ok {
			t.Errorf("%s should be unset, got %v", k, v[k])
		}
	}
	if v["minScore"] != 64 { // default 6.5 → > 64
		t.Errorf("minScore: got %v, want 64", v["minScore"])
	}
}
