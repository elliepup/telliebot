package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

// kindOfID returns which kind (and so which source) a Result ID belongs to.
func kindOfID(id string) (pick.Kind, bool) {
	switch {
	case strings.HasPrefix(id, "anilist:"):
		return pick.Anime, true
	case strings.HasPrefix(id, "tmdb:movie:"):
		return pick.Movie, true
	case strings.HasPrefix(id, "tmdb:tv:"):
		return pick.Show, true
	}
	return "", false
}

// handleMore replaces a pick with a random recommendation for it. The
// rating filter comes from the message's Reroll button, so it matches what
// was asked for; mood, length, and era are dropped because recommendation
// lists are too short to filter that hard. Reroll still uses the original
// request, so it gets you out of a "more like this" chain.
func (b *Bot) handleMore(i *discordgo.InteractionCreate, key string) {
	if !b.ack(i, discordgo.InteractionResponseDeferredMessageUpdate) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()

	scope := scopeOf(i)
	title := b.title(ctx, key)

	kind, _ := kindOfID(key)
	rec, ok := b.sources[kind].(pick.Recommender)
	if !ok {
		b.ephemeral(i, "Can't look up similar titles for that one.", nil)
		return
	}

	req, ok := rerollRequest(i.Message)
	if !ok {
		req = request{Mood: mood.Surprise.Key, Kind: kindAny}
	}
	f := similarFilters(req, key)
	if seen, err := b.store.SeenKeys(ctx, scope); err != nil {
		slog.Warn("load seen list; recommending without it", "err", err)
	} else {
		for k := range seen {
			f.Exclude[k] = true
		}
	}

	res, err := rec.Similar(ctx, key, f)
	if err != nil {
		if errors.Is(err, pick.ErrNoResults) {
			b.ephemeral(i, fmt.Sprintf("No recommendations like **%s** left that fit your filters. Try 🔄 Reroll?", escape(title)), nil)
			return
		}
		slog.Warn("similar failed", "id", key, "err", err)
		b.ephemeral(i, "Couldn't reach the catalog right now. Try again in a sec.", nil)
		return
	}
	if err := b.store.SaveItem(ctx, res); err != nil {
		slog.Warn("cache item", "id", res.ID, "err", err)
	}
	saved, err := b.store.InWatchlist(ctx, scope, res.ID)
	if err != nil {
		slog.Warn("check watchlist", "err", err)
	}

	note := fmt.Sprintf("✨ More like **%s**", escape(title))
	embeds := []*discordgo.MessageEmbed{similarEmbed(res, title, req)}
	components := []discordgo.MessageComponent{buttonRow(req, &res, saved)}
	edit := &discordgo.WebhookEdit{Content: &note, Embeds: &embeds, Components: &components}
	if _, err := b.s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Error("edit interaction response", "err", err)
	}
}

// similarFilters keeps only the rating from req, and always excludes the
// title the recommendations are for.
func similarFilters(req request, key string) pick.Filters {
	rating := req.Rating
	if req.From == fromWatchlist {
		rating = "" // watchlist picks ignore filters, so use the default
	}
	return pick.Filters{
		MinRating: pick.ParseFilters(rating, "", "").MinRating,
		Exclude:   map[string]bool{key: true},
	}
}

// similarEmbed is the usual card with a footer that says what it's based
// on, since the mood and length/era filters weren't used.
func similarEmbed(res pick.Result, likeTitle string, req request) *discordgo.MessageEmbed {
	e := toEmbed(res, mood.Surprise, req)
	footer := []string{"✨ Like " + likeTitle, res.Kind.Label()}
	if req.From != fromWatchlist {
		footer = append(footer, pick.Describe(req.Rating, "", "")...)
	}
	footer = append(footer, "via "+res.Source)
	e.Footer = &discordgo.MessageEmbedFooter{Text: truncate(strings.Join(footer, " · "), 2048)}
	return e
}
