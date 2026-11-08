package anilist

import (
	"strings"
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

func TestKeepSimilar(t *testing.T) {
	f := pick.Filters{MinRating: 7.5, Exclude: map[string]bool{"anilist:2": true}}
	ok := media{ID: 1, Format: "TV", AverageScore: 75}
	cases := []struct {
		name string
		m    func(media) media
		want bool
	}{
		{"passes at the exact minimum", func(m media) media { return m }, true},
		{"below rating", func(m media) media { m.AverageScore = 74; return m }, false},
		{"unscored", func(m media) media { m.AverageScore = 0; return m }, false},
		{"excluded", func(m media) media { m.ID = 2; return m }, false},
		{"adult", func(m media) media { m.IsAdult = true; return m }, false},
		{"OVA", func(m media) media { m.Format = "OVA"; return m }, false},
		{"movie", func(m media) media { m.Format = "MOVIE"; return m }, true},
	}
	for _, c := range cases {
		if got := keepSimilar(c.m(ok), f); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseItemID(t *testing.T) {
	if n, ok := parseItemID("anilist:21"); !ok || n != 21 {
		t.Errorf("got %d %v", n, ok)
	}
	for _, bad := range []string{"tmdb:movie:21", "anilist:", "anilist:x", "anilist:-3"} {
		if _, ok := parseItemID(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestRecsQueryUsesSharedFields(t *testing.T) {
	for _, q := range []string{query, recsQuery} {
		if !strings.Contains(q, "trailer { id site }") || !strings.Contains(q, "isAdult") {
			t.Errorf("query is missing shared media fields:\n%s", q)
		}
	}
}
