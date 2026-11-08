package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestButtonRow(t *testing.T) {
	req := request{Mood: "cozy", Kind: "anime"}

	if n := len(buttonRow(req, "").Components); n != 1 {
		t.Fatalf("no trailer: got %d buttons, want 1", n)
	}

	row := buttonRow(req, "https://www.youtube.com/watch?v=abc")
	if n := len(row.Components); n != 2 {
		t.Fatalf("with trailer: got %d buttons, want 2", n)
	}
	link, ok := row.Components[1].(discordgo.Button)
	if !ok || link.Style != discordgo.LinkButton || link.URL == "" || link.CustomID != "" {
		t.Fatalf("trailer button should be a link button with a URL and no custom ID: %+v", row.Components[1])
	}
}
