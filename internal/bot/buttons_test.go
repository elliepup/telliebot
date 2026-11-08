package bot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/pick"
	"github.com/elliepup/telliebot/internal/storage"
)

func buttonsOf(t *testing.T, row discordgo.ActionsRow) []discordgo.Button {
	t.Helper()
	var out []discordgo.Button
	for _, c := range row.Components {
		b, ok := c.(discordgo.Button)
		if !ok {
			t.Fatalf("unexpected component %T", c)
		}
		out = append(out, b)
	}
	return out
}

func TestButtonRowOnError(t *testing.T) {
	bs := buttonsOf(t, buttonRow(request{Mood: "cozy", Kind: "anime"}, nil, false))
	if len(bs) != 1 || bs[0].Label != "Reroll" {
		t.Fatalf("got %+v, want only Reroll", bs)
	}
}

func TestButtonRowFull(t *testing.T) {
	res := &pick.Result{ID: "tmdb:movie:603", TrailerURL: "https://www.youtube.com/watch?v=abc"}
	bs := buttonsOf(t, buttonRow(request{Mood: "cozy", Kind: "movie"}, res, false))

	labels := []string{}
	for _, b := range bs {
		labels = append(labels, b.Label)
	}
	if got := strings.Join(labels, ","); got != "Reroll,Seen it,Save,Trailer" {
		t.Fatalf("buttons: got %s", got)
	}
	if bs[1].CustomID != "seen:tmdb:movie:603" || bs[2].CustomID != "save:tmdb:movie:603" {
		t.Fatalf("custom IDs: %q %q", bs[1].CustomID, bs[2].CustomID)
	}
	if bs[3].Style != discordgo.LinkButton || bs[3].URL == "" || bs[3].CustomID != "" {
		t.Fatalf("trailer should be a link button: %+v", bs[3])
	}
	if len(bs) > 5 {
		t.Fatalf("Discord allows at most 5 buttons per row, got %d", len(bs))
	}
}

func TestButtonRowSavedAndNoTrailer(t *testing.T) {
	bs := buttonsOf(t, buttonRow(request{}, &pick.Result{ID: "anilist:1"}, true))
	if len(bs) != 3 {
		t.Fatalf("got %d buttons, want 3", len(bs))
	}
	if bs[2].Label != "Saved" || !bs[2].Disabled {
		t.Fatalf("save button should show Saved and be disabled: %+v", bs[2])
	}
}

// decodedRow round-trips a row through JSON so components come back the way
// discordgo decodes them from Discord (as pointers).
func decodedRow(t *testing.T, row discordgo.ActionsRow) []discordgo.MessageComponent {
	t.Helper()
	raw, err := json.Marshal(struct {
		Components []discordgo.MessageComponent `json:"components"`
	}{[]discordgo.MessageComponent{row}})
	if err != nil {
		t.Fatal(err)
	}
	var msg discordgo.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	return msg.Components
}

func TestRerollRequestFromDecodedMessage(t *testing.T) {
	req := request{Mood: "spooky", Kind: "show", Rating: "8", Era: "recent", From: ""}
	comps := decodedRow(t, buttonRow(req, &pick.Result{ID: "tmdb:tv:1"}, false))
	got, ok := rerollRequest(&discordgo.Message{Components: comps})
	if !ok || got != req {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, req)
	}
	if _, ok := rerollRequest(nil); ok {
		t.Fatal("nil message should not yield a request")
	}
}

func TestMapButtonsMarksSaved(t *testing.T) {
	comps := decodedRow(t, buttonRow(request{Mood: "cozy"}, &pick.Result{ID: "anilist:5"}, false))
	out := mapButtons(comps, func(btn discordgo.Button) discordgo.Button {
		if btn.CustomID == savePrefix+"anilist:5" {
			return savedButton("anilist:5")
		}
		return btn
	})
	bs := buttonsOf(t, out[0].(discordgo.ActionsRow))
	if bs[2].Label != "Saved" || !bs[2].Disabled {
		t.Fatalf("save button not flipped: %+v", bs[2])
	}
	if bs[0].Label != "Reroll" || bs[0].CustomID == "" {
		t.Fatalf("other buttons should be untouched: %+v", bs[0])
	}
}

func TestEscape(t *testing.T) {
	if got := escape("*Re:Zero* [x]_y_"); got != `\*Re:Zero\* \[x\]\_y\_` {
		t.Fatalf("got %s", got)
	}
}

func TestWatchlistViewEmpty(t *testing.T) {
	embed, comps := watchlistView(nil, 0, "")
	if !strings.Contains(embed.Description, "Nothing saved") || len(comps) != 0 {
		t.Fatalf("empty view: %q, %d components", embed.Description, len(comps))
	}
}

func TestWatchlistViewFitsDiscordLimits(t *testing.T) {
	var entries []storage.Entry
	for n := 0; n < watchlistPage; n++ {
		entries = append(entries, storage.Entry{
			Item: pick.Result{
				ID:    fmt.Sprintf("tmdb:movie:%d", n),
				Kind:  pick.Movie,
				Title: strings.Repeat("Very Long Title ", 10), // 160 chars
				URL:   fmt.Sprintf("https://www.themoviedb.org/movie/%d", n),
			},
			AddedBy: "123456789012345678",
			AddedAt: time.Now(),
		})
	}
	embed, comps := watchlistView(entries, 40, "movie")

	if n := len(embed.Description); n > 4096 {
		t.Fatalf("description is %d chars (max 4096)", n)
	}
	if !strings.Contains(embed.Footer.Text, "of 40") {
		t.Fatalf("footer should mention the total: %q", embed.Footer.Text)
	}
	menu := comps[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if len(menu.Options) > 25 || menu.MaxValues != len(menu.Options) {
		t.Fatalf("menu: %d options, max %d", len(menu.Options), menu.MaxValues)
	}
	for _, o := range menu.Options {
		if len([]rune(o.Label)) > 100 || len(o.Value) > 100 {
			t.Fatalf("option over 100 chars: %+v", o)
		}
	}
	if menu.CustomID != "wlremove:movie" {
		t.Fatalf("menu custom ID: %q", menu.CustomID)
	}
}
