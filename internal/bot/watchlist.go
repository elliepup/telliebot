package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/pick"
	"github.com/elliepup/telliebot/internal/storage"
)

const (
	// watchlistPage is how many entries /watchlist shows. It matches
	// Discord's 25-option limit for the Remove dropdown.
	watchlistPage = 25
	// Embed descriptions max out at 4096 characters; stay well under.
	maxDescription = 3800
)

func (b *Bot) handleWatchlist(i *discordgo.InteractionCreate, kind string) {
	if !b.ack(i, discordgo.InteractionResponseDeferredChannelMessageWithSource) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()
	b.renderWatchlist(ctx, i, kind, "")
}

// handleWatchlistRemove removes the titles chosen in the dropdown, then
// redraws the list in place.
func (b *Bot) handleWatchlistRemove(i *discordgo.InteractionCreate, kind string, keys []string) {
	if !b.ack(i, discordgo.InteractionResponseDeferredMessageUpdate) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()

	scope := scopeOf(i)
	var removed []string
	for _, k := range keys {
		if err := b.store.RemoveFromWatchlist(ctx, scope, k); err != nil {
			slog.Error("remove from watchlist", "id", k, "err", err)
			continue
		}
		removed = append(removed, "**"+escape(b.title(ctx, k))+"**")
	}
	note := ""
	if len(removed) > 0 {
		note = fmt.Sprintf("🗑️ %s removed %s.", escape(displayName(i)), strings.Join(removed, ", "))
	}
	b.renderWatchlist(ctx, i, kind, note)
}

func (b *Bot) renderWatchlist(ctx context.Context, i *discordgo.InteractionCreate, kind, note string) {
	entries, total, err := b.store.Watchlist(ctx, scopeOf(i), pick.Kind(kind), watchlistPage)
	if err != nil {
		slog.Error("load watchlist", "err", err)
		msg := "Couldn't load the watchlist. Try again in a sec."
		b.s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	embed, components := watchlistView(entries, total, kind)
	edit := &discordgo.WebhookEdit{
		Content:    &note,
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &components,
	}
	if _, err := b.s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Error("render watchlist", "err", err)
	}
}

// watchlistView builds the /watchlist embed and its Remove dropdown.
func watchlistView(entries []storage.Entry, total int, kind string) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	title := "⭐ Watchlist"
	if kind != "" {
		title += " · " + pick.Kind(kind).Label()
	}
	embed := &discordgo.MessageEmbed{Title: title, Color: 0xf1c40f}

	if total == 0 {
		embed.Description = "Nothing saved yet. Hit ⭐ **Save** on a pick to add it."
		return embed, []discordgo.MessageComponent{}
	}

	var lines []string
	shown := 0
	length := 0
	for n, e := range entries {
		name := "**" + escape(truncate(e.Item.Title, 80)) + "**"
		if e.Item.URL != "" {
			name = "[" + name + "](" + e.Item.URL + ")"
		}
		// Mentions in embeds render as names but never ping.
		line := fmt.Sprintf("`%2d.` %s · %s · <@%s>", n+1, name, e.Item.Kind.Label(), e.AddedBy)
		if length+len(line)+1 > maxDescription {
			break
		}
		lines = append(lines, line)
		length += len(line) + 1
		shown++
	}
	embed.Description = strings.Join(lines, "\n")

	if shown < total {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Showing the %d newest of %d · /pick from:watchlist picks one at random", shown, total)}
	} else {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("%d saved · /pick from:watchlist picks one at random", total)}
	}

	options := make([]discordgo.SelectMenuOption, 0, shown)
	for _, e := range entries[:shown] {
		options = append(options, discordgo.SelectMenuOption{
			Label:       truncate(e.Item.Title, 100),
			Value:       e.Item.ID,
			Description: e.Item.Kind.Label(),
		})
	}
	one := 1
	menu := discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    wlRemoveID + kind,
		Placeholder: "🗑️ Remove from watchlist…",
		MinValues:   &one,
		MaxValues:   len(options),
		Options:     options,
	}
	return embed, []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{menu}}}
}

// truncate shortens s to at most max runes, adding an ellipsis if cut.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
