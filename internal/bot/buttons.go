package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/pick"
)

// Custom ID prefixes. Title IDs contain ':' themselves, so everything after
// the prefix is the ID.
const (
	rerollPrefix = "reroll"
	seenPrefix   = "seen:"
	savePrefix   = "save:"
	unseenPrefix = "unseen:"   // unseen:<w|->:<id>; w = restore to watchlist
	wlRemoveID   = "wlremove:" // wlremove:<kind filter>
)

func (b *Bot) onComponent(i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	switch {
	case strings.HasPrefix(id, rerollPrefix+":"):
		if req, ok := parseCustomID(id); ok {
			b.respond(i, discordgo.InteractionResponseDeferredMessageUpdate, req)
		}
	case strings.HasPrefix(id, seenPrefix):
		b.handleSeen(i, strings.TrimPrefix(id, seenPrefix))
	case strings.HasPrefix(id, savePrefix):
		b.handleSave(i, strings.TrimPrefix(id, savePrefix))
	case strings.HasPrefix(id, unseenPrefix):
		b.handleUnseen(i, strings.TrimPrefix(id, unseenPrefix))
	case strings.HasPrefix(id, wlRemoveID):
		b.handleWatchlistRemove(i, strings.TrimPrefix(id, wlRemoveID), i.MessageComponentData().Values)
	}
}

// buttonRow builds the buttons under a pick. With no result (an error
// message) only Reroll is shown.
func buttonRow(req request, res *pick.Result, saved bool) discordgo.ActionsRow {
	buttons := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Reroll",
			Style:    discordgo.PrimaryButton,
			CustomID: req.customID(),
			Emoji:    &discordgo.ComponentEmoji{Name: "🔄"},
		},
	}
	if res == nil {
		return discordgo.ActionsRow{Components: buttons}
	}

	buttons = append(buttons, discordgo.Button{
		Label:    "Seen it",
		Style:    discordgo.SecondaryButton,
		CustomID: seenPrefix + res.ID,
		Emoji:    &discordgo.ComponentEmoji{Name: "👁️"},
	})
	if saved {
		buttons = append(buttons, savedButton(res.ID))
	} else {
		buttons = append(buttons, discordgo.Button{
			Label:    "Save",
			Style:    discordgo.SecondaryButton,
			CustomID: savePrefix + res.ID,
			Emoji:    &discordgo.ComponentEmoji{Name: "⭐"},
		})
	}
	// Link buttons open the URL directly and send no interaction.
	if res.TrailerURL != "" {
		buttons = append(buttons, discordgo.Button{
			Label: "Trailer",
			Style: discordgo.LinkButton,
			URL:   res.TrailerURL,
			Emoji: &discordgo.ComponentEmoji{Name: "▶️"},
		})
	}
	return discordgo.ActionsRow{Components: buttons}
}

func savedButton(id string) discordgo.Button {
	return discordgo.Button{
		Label:    "Saved",
		Style:    discordgo.SuccessButton,
		CustomID: savePrefix + id,
		Emoji:    &discordgo.ComponentEmoji{Name: "⭐"},
		Disabled: true,
	}
}

// mapButtons rebuilds message components, passing every button through fn.
// Components decoded from Discord arrive as pointers; ones we built are
// values, so both are handled.
func mapButtons(rows []discordgo.MessageComponent, fn func(discordgo.Button) discordgo.Button) []discordgo.MessageComponent {
	out := make([]discordgo.MessageComponent, 0, len(rows))
	for _, row := range rows {
		var ar discordgo.ActionsRow
		switch r := row.(type) {
		case *discordgo.ActionsRow:
			ar = *r
		case discordgo.ActionsRow:
			ar = r
		default:
			out = append(out, row)
			continue
		}
		comps := make([]discordgo.MessageComponent, 0, len(ar.Components))
		for _, c := range ar.Components {
			switch btn := c.(type) {
			case *discordgo.Button:
				comps = append(comps, fn(*btn))
			case discordgo.Button:
				comps = append(comps, fn(btn))
			default:
				comps = append(comps, c)
			}
		}
		out = append(out, discordgo.ActionsRow{Components: comps})
	}
	return out
}

// rerollRequest recovers the original request from a pick message's
// Reroll button.
func rerollRequest(msg *discordgo.Message) (request, bool) {
	if msg == nil {
		return request{}, false
	}
	var req request
	found := false
	mapButtons(msg.Components, func(btn discordgo.Button) discordgo.Button {
		if !found {
			req, found = parseCustomID(btn.CustomID)
		}
		return btn
	})
	return req, found
}

// handleSeen marks a title seen, replaces the pick with a fresh one, and
// privately offers the clicker an Undo.
func (b *Bot) handleSeen(i *discordgo.InteractionCreate, key string) {
	if !b.ack(i, discordgo.InteractionResponseDeferredMessageUpdate) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()

	scope, user := scopeOf(i), userOf(i)
	wasSaved, err := b.store.MarkSeen(ctx, scope, key, user.ID)
	if err != nil {
		slog.Error("mark seen", "id", key, "err", err)
		b.ephemeral(i, "Couldn't mark that as seen. Try again?", nil)
		return
	}

	title := b.title(ctx, key)
	if req, ok := rerollRequest(i.Message); ok {
		note := fmt.Sprintf("👁️ %s marked **%s** as seen.", escape(displayName(i)), escape(title))
		b.pickAndRender(ctx, i, req, note)
	}

	restore := "-"
	if wasSaved {
		restore = "w"
	}
	undo := discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Undo",
			Style:    discordgo.SecondaryButton,
			CustomID: unseenPrefix + restore + ":" + key,
			Emoji:    &discordgo.ComponentEmoji{Name: "↩️"},
		},
	}}
	msg := fmt.Sprintf("Marked **%s** as seen. It won't come up again.", escape(title))
	if wasSaved {
		msg += " (Also removed from your watchlist.)"
	}
	b.ephemeral(i, msg, []discordgo.MessageComponent{undo})
}

// handleUnseen undoes a Seen it click from the ephemeral Undo button.
func (b *Bot) handleUnseen(i *discordgo.InteractionCreate, rest string) {
	restore, key, ok := strings.Cut(rest, ":")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()

	scope := scopeOf(i)
	msg := ""
	if err := b.store.UnmarkSeen(ctx, scope, key); err != nil {
		slog.Error("unmark seen", "id", key, "err", err)
		msg = "Couldn't undo that. Try again?"
	} else {
		msg = fmt.Sprintf("↩️ Undone. **%s** can come up again.", escape(b.title(ctx, key)))
		if restore == "w" {
			if err := b.store.AddToWatchlist(ctx, scope, key, userOf(i).ID); err != nil {
				slog.Error("restore watchlist", "id", key, "err", err)
			} else {
				msg += " It's back on your watchlist too."
			}
		}
	}

	err := b.s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: msg, Components: []discordgo.MessageComponent{}},
	})
	if err != nil {
		slog.Error("respond to undo", "err", err)
	}
}

// handleSave adds a title to the watchlist and flips the button to Saved.
func (b *Bot) handleSave(i *discordgo.InteractionCreate, key string) {
	if !b.ack(i, discordgo.InteractionResponseDeferredMessageUpdate) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()

	if err := b.store.AddToWatchlist(ctx, scopeOf(i), key, userOf(i).ID); err != nil {
		slog.Error("add to watchlist", "id", key, "err", err)
		b.ephemeral(i, "Couldn't save that. Try again?", nil)
		return
	}

	comps := mapButtons(i.Message.Components, func(btn discordgo.Button) discordgo.Button {
		if btn.CustomID == savePrefix+key {
			return savedButton(key)
		}
		return btn
	})
	if _, err := b.s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Components: &comps}); err != nil {
		slog.Error("mark saved", "err", err)
	}
}

// title returns a cached title's name, falling back to its ID.
func (b *Bot) title(ctx context.Context, key string) string {
	if r, ok, err := b.store.Item(ctx, key); err == nil && ok {
		return r.Title
	}
	return key
}

// ephemeral sends a follow-up only the clicking user can see.
func (b *Bot) ephemeral(i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent) {
	_, err := b.s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content:    content,
		Flags:      discordgo.MessageFlagsEphemeral,
		Components: components,
	})
	if err != nil {
		slog.Error("send ephemeral follow-up", "err", err)
	}
}
