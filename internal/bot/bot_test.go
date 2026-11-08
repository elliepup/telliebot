package bot

import (
	"strings"
	"testing"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

func TestCustomIDRoundTrip(t *testing.T) {
	req := request{Mood: "cozy", Kind: "movie", Rating: "7.5", Length: "short", Era: "2010s"}
	got, ok := parseCustomID(req.customID())
	if !ok || got != req {
		t.Fatalf("got %+v (ok=%v), want %+v", got, ok, req)
	}
}

func TestCustomIDRoundTripEmptyFilters(t *testing.T) {
	req := request{Mood: "surprise", Kind: "any"}
	got, ok := parseCustomID(req.customID())
	if !ok || got != req {
		t.Fatalf("got %+v (ok=%v), want %+v", got, ok, req)
	}
}

func TestParseLegacyCustomID(t *testing.T) {
	got, ok := parseCustomID("reroll:hype:anime")
	if !ok || got != (request{Mood: "hype", Kind: "anime"}) {
		t.Fatalf("got %+v (ok=%v)", got, ok)
	}
}

func TestParseCustomIDRejectsOthers(t *testing.T) {
	for _, id := range []string{"", "other:a:b", "reroll:a", "reroll:a:b:c:d"} {
		if _, ok := parseCustomID(id); ok {
			t.Errorf("%q should be rejected", id)
		}
	}
}

// Discord caps custom IDs at 100 characters and choice names at 100.
func TestDiscordLimits(t *testing.T) {
	longest := request{Kind: "movie"}
	for _, m := range mood.All {
		if len(m.Key) > len(longest.Mood) {
			longest.Mood = m.Key
		}
	}
	for _, set := range []struct {
		field   *string
		choices []pick.Choice
	}{{&longest.Rating, pick.RatingChoices}, {&longest.Length, pick.LengthChoices}, {&longest.Era, pick.EraChoices}} {
		for _, c := range set.choices {
			if strings.Contains(c.Key, ":") {
				t.Errorf("choice key %q contains ':'", c.Key)
			}
			if len(c.Key) > len(*set.field) {
				*set.field = c.Key
			}
			if n := len([]rune(c.Label)); n > 100 {
				t.Errorf("choice label %q is %d chars (max 100)", c.Label, n)
			}
		}
	}
	if n := len(longest.customID()); n > 100 {
		t.Errorf("custom ID %q is %d chars (max 100)", longest.customID(), n)
	}
}
