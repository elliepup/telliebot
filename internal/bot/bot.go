// Package bot wires telliebot's slash commands to the title sources.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

const (
	kindAny      = "any"
	rerollPrefix = "reroll"
	pickTimeout  = 15 * time.Second
)

var errUnavailable = errors.New("source not configured")

var defaultColors = map[pick.Kind]int{
	pick.Anime: 0x02a9ff, // AniList blue
	pick.Movie: 0xf5c518,
	pick.Show:  0x9b59b6,
}

// request is everything the user asked for in one /pick. It round-trips
// through the reroll button's custom ID, so keys must not contain ':'.
type request struct {
	Mood, Kind, Rating, Length, Era string
}

func (r request) customID() string {
	return strings.Join([]string{rerollPrefix, r.Mood, r.Kind, r.Rating, r.Length, r.Era}, ":")
}

// parseCustomID reads a reroll button ID. It also accepts the original
// 3-part form (reroll:mood:kind) so buttons on older messages keep working.
func parseCustomID(id string) (request, bool) {
	p := strings.Split(id, ":")
	if p[0] != rerollPrefix {
		return request{}, false
	}
	switch len(p) {
	case 3:
		return request{Mood: p[1], Kind: p[2]}, true
	case 6:
		return request{Mood: p[1], Kind: p[2], Rating: p[3], Length: p[4], Era: p[5]}, true
	}
	return request{}, false
}

// Bot is a running Discord session.
type Bot struct {
	s       *discordgo.Session
	guildID string
	sources map[pick.Kind]pick.Source
	kinds   []pick.Kind // configured kinds, sorted, for stable choices
}

// New creates a bot. Call Open to connect.
func New(token, guildID string, sources map[pick.Kind]pick.Source) (*Bot, error) {
	if len(sources) == 0 {
		return nil, errors.New("no title sources configured")
	}
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsGuilds

	b := &Bot{s: s, guildID: guildID, sources: sources}
	for k := range sources {
		b.kinds = append(b.kinds, k)
	}
	sort.Slice(b.kinds, func(i, j int) bool { return b.kinds[i] < b.kinds[j] })

	s.AddHandler(b.onInteraction)
	return b, nil
}

// Open connects to Discord and registers slash commands.
func (b *Bot) Open() error {
	if err := b.s.Open(); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}
	cmds, err := b.s.ApplicationCommandBulkOverwrite(b.s.State.User.ID, b.guildID, b.commands())
	if err != nil {
		b.s.Close()
		return fmt.Errorf("register commands: %w", err)
	}
	slog.Info("connected", "user", b.s.State.User.Username, "commands", len(cmds), "guild", b.guildID)
	return nil
}

// Close disconnects from Discord.
func (b *Bot) Close() error { return b.s.Close() }

func toOptionChoices(cs []pick.Choice) []*discordgo.ApplicationCommandOptionChoice {
	out := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(cs))
	for _, c := range cs {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: c.Label, Value: c.Key})
	}
	return out
}

func (b *Bot) commands() []*discordgo.ApplicationCommand {
	moodChoices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(mood.All))
	for _, m := range mood.All {
		moodChoices = append(moodChoices, &discordgo.ApplicationCommandOptionChoice{
			Name: m.Emoji + " " + m.Label, Value: m.Key,
		})
	}

	typeChoices := []*discordgo.ApplicationCommandOptionChoice{{Name: "Anything", Value: kindAny}}
	for _, k := range b.kinds {
		typeChoices = append(typeChoices, &discordgo.ApplicationCommandOptionChoice{
			Name: k.Label(), Value: string(k),
		})
	}

	str := discordgo.ApplicationCommandOptionString
	return []*discordgo.ApplicationCommand{{
		Name:        "pick",
		Description: "Pick something random to watch",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: str, Name: "mood", Description: "How are you feeling?", Choices: moodChoices},
			{Type: str, Name: "type", Description: "What kind of thing to watch", Choices: typeChoices},
			{Type: str, Name: "min_rating", Description: fmt.Sprintf("Minimum rating out of 10 (default %.1f)", pick.DefaultMinRating), Choices: toOptionChoices(pick.RatingChoices)},
			{Type: str, Name: "length", Description: "How much time you've got", Choices: toOptionChoices(pick.LengthChoices)},
			{Type: str, Name: "era", Description: "When it came out", Choices: toOptionChoices(pick.EraChoices)},
		},
	}}
}

func (b *Bot) onInteraction(_ *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		data := i.ApplicationCommandData()
		if data.Name != "pick" {
			return
		}
		req := request{Mood: mood.Surprise.Key, Kind: kindAny}
		for _, o := range data.Options {
			switch o.Name {
			case "mood":
				req.Mood = o.StringValue()
			case "type":
				req.Kind = o.StringValue()
			case "min_rating":
				req.Rating = o.StringValue()
			case "length":
				req.Length = o.StringValue()
			case "era":
				req.Era = o.StringValue()
			}
		}
		b.respond(i, discordgo.InteractionResponseDeferredChannelMessageWithSource, req)

	case discordgo.InteractionMessageComponent:
		req, ok := parseCustomID(i.MessageComponentData().CustomID)
		if !ok {
			return
		}
		b.respond(i, discordgo.InteractionResponseDeferredMessageUpdate, req)
	}
}

// respond acknowledges the interaction right away (Discord allows 3s), then
// fetches a pick and edits the response with it.
func (b *Bot) respond(i *discordgo.InteractionCreate, ack discordgo.InteractionResponseType, req request) {
	if err := b.s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: ack}); err != nil {
		slog.Error("ack interaction", "err", err)
		return
	}

	m, ok := mood.Get(req.Mood)
	if !ok {
		m = mood.Surprise
	}
	f := pick.ParseFilters(req.Rating, req.Length, req.Era)

	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()

	res, err := b.pick(ctx, req.Kind, m, f)
	components := []discordgo.MessageComponent{buttonRow(req, res.TrailerURL)}

	edit := &discordgo.WebhookEdit{Components: &components}
	if err != nil {
		slog.Warn("pick failed", "request", req, "err", err)
		msg := "Couldn't reach the catalog right now. Try again in a sec."
		switch {
		case errors.Is(err, pick.ErrNoResults):
			msg = "Nothing matched that. Try a different mood or loosen the filters?"
		case errors.Is(err, errUnavailable):
			msg = "That type isn't set up yet."
		}
		edit.Content = &msg
		edit.Embeds = &[]*discordgo.MessageEmbed{}
	} else {
		empty := ""
		edit.Content = &empty
		edit.Embeds = &[]*discordgo.MessageEmbed{toEmbed(res, m, req)}
	}

	if _, err := b.s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Error("edit interaction response", "err", err)
	}
}

func (b *Bot) pick(ctx context.Context, kind string, m mood.Mood, f pick.Filters) (pick.Result, error) {
	k := pick.Kind(kind)
	if kind == kindAny {
		k = b.kinds[rand.IntN(len(b.kinds))]
	}
	src, ok := b.sources[k]
	if !ok {
		return pick.Result{}, errUnavailable
	}
	return src.Pick(ctx, m, f)
}

// buttonRow is the Reroll button, plus a Trailer link button when there is a
// trailer. Link buttons open the URL directly and send no interaction.
func buttonRow(req request, trailerURL string) discordgo.ActionsRow {
	buttons := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Reroll",
			Style:    discordgo.PrimaryButton,
			CustomID: req.customID(),
			Emoji:    &discordgo.ComponentEmoji{Name: "🔄"},
		},
	}
	if trailerURL != "" {
		buttons = append(buttons, discordgo.Button{
			Label: "Trailer",
			Style: discordgo.LinkButton,
			URL:   trailerURL,
			Emoji: &discordgo.ComponentEmoji{Name: "▶️"},
		})
	}
	return discordgo.ActionsRow{Components: buttons}
}

func toEmbed(r pick.Result, m mood.Mood, req request) *discordgo.MessageEmbed {
	color := r.Color
	if color == 0 {
		color = defaultColors[r.Kind]
	}

	fields := make([]*discordgo.MessageEmbedField, 0, len(r.Fields))
	for _, f := range r.Fields {
		fields = append(fields, &discordgo.MessageEmbedField{Name: f.Name, Value: f.Value, Inline: true})
	}

	footer := []string{m.Emoji + " " + m.Label, r.Kind.Label()}
	footer = append(footer, pick.Describe(req.Rating, req.Length, req.Era)...)
	footer = append(footer, "via "+r.Source)

	e := &discordgo.MessageEmbed{
		Title:       r.Title,
		URL:         r.URL,
		Description: r.Description,
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: strings.Join(footer, " · ")},
	}
	if r.ImageURL != "" {
		e.Image = &discordgo.MessageEmbedImage{URL: r.ImageURL}
	}
	return e
}
