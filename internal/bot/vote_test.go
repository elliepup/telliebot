package bot

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

func newTestVote(n int) *vote {
	m, _ := mood.Get("cozy")
	v := &vote{
		id: "abcd1234", req: request{Mood: "cozy", Kind: kindAny, Rating: "8"}, mood: m,
		ballots: map[string]int{}, names: map[string]string{},
	}
	for k := 0; k < n; k++ {
		v.options = append(v.options, pick.Result{
			ID:          fmt.Sprintf("tmdb:movie:%d", 1000+k),
			Kind:        pick.Movie,
			Title:       strings.Repeat("An Extremely Long Movie Title ", 4), // 120 chars
			URL:         "https://www.themoviedb.org/movie/1",
			Description: strings.Repeat("A long description. ", 30),
			ImageURL:    "https://image.tmdb.org/t/p/w500/x.jpg",
			TrailerURL:  "https://www.youtube.com/watch?v=abc",
			Fields:      []pick.Field{{Name: "Rating", Value: "8.1/10"}, {Name: "Year", Value: "2019"}, {Name: "Genres", Value: "Drama, Comedy"}},
			Source:      "TMDB",
		})
	}
	return v
}

func TestCastSwitchAndToggle(t *testing.T) {
	v := newTestVote(3)
	v.cast("u1", "Nick", 0)
	v.cast("u2", "Sam", 2)
	v.cast("u1", "Nick", 1) // switch
	if c := v.counts(); c[0] != 0 || c[1] != 1 || c[2] != 1 {
		t.Fatalf("after switch: %v", c)
	}
	if strings.Join(v.order, ",") != "u1,u2" {
		t.Fatalf("switching must keep voter order: %v", v.order)
	}
	v.cast("u1", "Nick", 1) // same option again takes the vote back
	if c := v.counts(); c[1] != 0 || len(v.order) != 1 || v.order[0] != "u2" {
		t.Fatalf("after toggle off: counts %v order %v", c, v.order)
	}
}

func TestWinner(t *testing.T) {
	first := func(int) int { return 0 }
	last := func(n int) int { return n - 1 }

	if w, how := winner([]int{1, 3, 2}, first); w != 1 || how != "votes" {
		t.Errorf("clear: got %d %s", w, how)
	}
	if w, how := winner([]int{2, 0, 2}, last); w != 2 || how != "tie" {
		t.Errorf("tie: got %d %s (want the last tied leader)", w, how)
	}
	if w, how := winner([]int{2, 0, 2}, first); w != 0 || how != "tie" {
		t.Errorf("tie: got %d %s (want the first tied leader)", w, how)
	}
	if w, how := winner([]int{0, 0, 0}, last); w != 2 || how != "none" {
		t.Errorf("none: got %d %s", w, how)
	}
}

func TestParseVoteTimer(t *testing.T) {
	cases := map[string]time.Duration{"": 2 * time.Minute, "30s": 30 * time.Second, "10m": 10 * time.Minute, "none": 0, "bogus": 2 * time.Minute}
	for in, want := range cases {
		if got := parseVoteTimer(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
	for _, tm := range voteTimers {
		if tm.D > untimedVoteLimit {
			t.Errorf("timer %s exceeds the interaction token lifetime", tm.Key)
		}
	}
}

func embedChars(es []*discordgo.MessageEmbed) int {
	n := 0
	for _, e := range es {
		n += len(e.Title) + len(e.Description)
		for _, f := range e.Fields {
			n += len(f.Name) + len(f.Value)
		}
		if e.Footer != nil {
			n += len(e.Footer.Text)
		}
	}
	return n
}

// checkLimits enforces Discord's message limits on a rendered view.
func checkLimits(t *testing.T, embeds []*discordgo.MessageEmbed, comps []discordgo.MessageComponent) {
	t.Helper()
	if len(embeds) > 10 {
		t.Errorf("%d embeds (max 10)", len(embeds))
	}
	if n := embedChars(embeds); n > 6000 {
		t.Errorf("embeds total %d chars (max 6000)", n)
	}
	for _, e := range embeds {
		if len([]rune(e.Title)) > 256 || len([]rune(e.Description)) > 4096 {
			t.Errorf("embed title/description too long: %d/%d", len(e.Title), len(e.Description))
		}
	}
	if len(comps) > 5 {
		t.Errorf("%d rows (max 5)", len(comps))
	}
	for _, row := range comps {
		ar := row.(discordgo.ActionsRow)
		if len(ar.Components) > 5 {
			t.Errorf("%d buttons in a row (max 5)", len(ar.Components))
		}
		for _, c := range ar.Components {
			b := c.(discordgo.Button)
			if len([]rune(b.Label)) > 80 || len(b.CustomID) > 100 {
				t.Errorf("button over limits: %q / %q", b.Label, b.CustomID)
			}
		}
	}
}

func TestActiveViewLimitsAndContent(t *testing.T) {
	v := newTestVote(maxVoteOptions)
	v.endsAt = time.Unix(1_900_000_000, 0)
	v.cast("u1", "Nick", 0)
	v.cast("u2", "Sam_the_*best*", 0)

	content, embeds, comps := v.activeView()
	checkLimits(t, embeds, comps)

	if !strings.Contains(content, "<t:1900000000:R>") {
		t.Errorf("timed vote should show a countdown: %q", content)
	}
	if !strings.Contains(content, `✅ 2 voted: Nick, Sam\_the\_\*best\*`) {
		t.Errorf("voter names missing or unescaped: %q", content)
	}
	if len(embeds) != maxVoteOptions || embeds[0].Thumbnail == nil || embeds[0].Image != nil {
		t.Errorf("want one compact card (thumbnail, no big image) per option")
	}
	if embeds[len(embeds)-1].Footer == nil || !strings.Contains(embeds[len(embeds)-1].Footer.Text, "⭐ 8+") {
		t.Errorf("last card should carry the filter footer")
	}
	vote1 := comps[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if vote1.CustomID != "vote:abcd1234:0" || !strings.HasSuffix(vote1.Label, "· 2") {
		t.Errorf("vote button: %+v", vote1)
	}
	end := comps[1].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	if end.CustomID != "voteend:abcd1234" {
		t.Errorf("end button: %+v", end)
	}
}

func TestActiveViewUntimed(t *testing.T) {
	content, _, _ := newTestVote(2).activeView()
	if !strings.Contains(content, "End vote") || strings.Contains(content, "<t:") {
		t.Errorf("untimed vote content: %q", content)
	}
}

func TestFinalView(t *testing.T) {
	v := newTestVote(4)
	v.options[2].Title = "Winner"
	v.cast("u1", "Nick", 2)
	v.cast("u2", "Sam", 2)
	v.cast("u3", "Alex", 0)

	content, embeds, comps := v.finalView(func(int) int { return 0 })
	checkLimits(t, embeds, comps)

	if !strings.Contains(content, "**Winner** wins with 2 votes") {
		t.Errorf("content: %q", content)
	}
	if embeds[0].Title != "Winner" || embeds[0].Image == nil {
		t.Errorf("first embed should be the full winner card: %+v", embeds[0])
	}
	if !strings.Contains(embeds[1].Description, "Winner** · 2 votes (Nick, Sam) 🏆") {
		t.Errorf("results should show who voted for what: %q", embeds[1].Description)
	}

	winRow := comps[0].(discordgo.ActionsRow).Components
	if winRow[0].(discordgo.Button).CustomID != "seen:tmdb:movie:1002" || winRow[1].(discordgo.Button).CustomID != "save:tmdb:movie:1002" {
		t.Errorf("winner buttons should target the winner: %+v", winRow)
	}
	others := comps[1].(discordgo.ActionsRow).Components
	if len(others) != 3 {
		t.Fatalf("want a Save button per runner-up (3), got %d", len(others))
	}
	if b := others[2].(discordgo.Button); b.Label != "Save 4️⃣" || b.CustomID != "save:tmdb:movie:1003" {
		t.Errorf("runner-up save: %+v", b)
	}
}

func TestFinalViewNoVotesAndWatchlist(t *testing.T) {
	v := newTestVote(3)
	v.req.From = fromWatchlist
	content, _, comps := v.finalView(func(int) int { return 1 })
	if !strings.Contains(content, "Nobody voted") {
		t.Errorf("content: %q", content)
	}
	if len(comps) != 1 {
		t.Errorf("watchlist votes need no runner-up Save row, got %d rows", len(comps))
	}
	if b := comps[0].(discordgo.ActionsRow).Components[1].(discordgo.Button); b.Label != "Saved" || !b.Disabled {
		t.Errorf("watchlist winner should already show Saved: %+v", b)
	}
}

func TestMarkSavedKeepsSuffix(t *testing.T) {
	b := markSaved("tmdb:movie:1")(discordgo.Button{Label: "Save 2️⃣", CustomID: "save:tmdb:movie:1"})
	if b.Label != "Saved 2️⃣" || !b.Disabled || b.Style != discordgo.SuccessButton {
		t.Fatalf("got %+v", b)
	}
	untouched := markSaved("tmdb:movie:1")(discordgo.Button{Label: "Save 3️⃣", CustomID: "save:tmdb:movie:2"})
	if untouched.Disabled {
		t.Fatal("other Save buttons must not change")
	}
}
