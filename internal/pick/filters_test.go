package pick

import (
	"reflect"
	"testing"
	"time"
)

func TestParseFiltersDefaults(t *testing.T) {
	got := ParseFilters("", "", "")
	want := Filters{MinRating: DefaultMinRating}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseFiltersRating(t *testing.T) {
	cases := map[string]float64{
		"8":    8,
		"7.5":  7.5,
		"junk": DefaultMinRating,
		"0":    DefaultMinRating,
		"11":   DefaultMinRating,
	}
	for in, want := range cases {
		if got := ParseFilters(in, "", "").MinRating; got != want {
			t.Errorf("rating %q: got %v, want %v", in, got, want)
		}
	}
}

func TestParseFiltersLength(t *testing.T) {
	f := ParseFilters("", "medium", "")
	if f.MovieMinutes != (Range{101, 140}) || f.AnimeEpisodes != (Range{14, 26}) || f.ShowEpisodeMinutes != (Range{31, 50}) {
		t.Fatalf("medium: got %+v", f)
	}
}

func TestParseFiltersEra(t *testing.T) {
	if got := ParseFilters("", "", "classic").Years; got != (Range{Max: 1999}) {
		t.Errorf("classic: got %+v", got)
	}
	if got := ParseFilters("", "", "2010s").Years; got != (Range{2010, 2019}) {
		t.Errorf("2010s: got %+v", got)
	}
	if got, want := ParseFilters("", "", "recent").Years, (Range{Min: time.Now().Year() - 2}); got != want {
		t.Errorf("recent: got %+v, want %+v", got, want)
	}
}

func TestChoiceKeysAreParsed(t *testing.T) {
	// Every choice offered in Discord must change the filters, or it's dead UI.
	base := ParseFilters("", "", "")
	for _, c := range RatingChoices {
		if ParseFilters(c.Key, "", "").MinRating == 0 {
			t.Errorf("rating choice %q not parsed", c.Key)
		}
	}
	for _, c := range LengthChoices {
		if reflect.DeepEqual(ParseFilters("", c.Key, ""), base) {
			t.Errorf("length choice %q not parsed", c.Key)
		}
	}
	for _, c := range EraChoices {
		if reflect.DeepEqual(ParseFilters("", "", c.Key), base) {
			t.Errorf("era choice %q not parsed", c.Key)
		}
	}
}

func TestDescribe(t *testing.T) {
	got := Describe("8", "", "2000s")
	want := []string{"⭐ 8+", "2000s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
