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

	return []*discordgo.ApplicationCommand{{
		Name:        "pick",
		Description: "Pick something random to watch",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "mood",
				Description: "How are you feeling?",
				Choices:     moodChoices,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "type",
				Description: "What kind of thing to watch",
				Choices:     typeChoices,
			},
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
		moodKey, kind := mood.Surprise.Key, kindAny
		for _, o := range data.Options {
			switch o.Name {
			case "mood":
				moodKey = o.StringValue()
			case "type":
				kind = o.StringValue()
			}
		}
		b.respond(i, discordgo.InteractionResponseDeferredChannelMessageWithSource, moodKey, kind)

	case discordgo.InteractionMessageComponent:
		parts := strings.Split(i.MessageComponentData().CustomID, ":")
		if len(parts) != 3 || parts[0] != rerollPrefix {
			return
		}
		b.respond(i, discordgo.InteractionResponseDeferredMessageUpdate, parts[1], parts[2])
	}
}

// respond acknowledges the interaction right away (Discord allows 3s), then
// fetches a pick and edits the response with it.
func (b *Bot) respond(i *discordgo.InteractionCreate, ack discordgo.InteractionResponseType, moodKey, kind string) {
	if err := b.s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: ack}); err != nil {
		slog.Error("ack interaction", "err", err)
		return
	}

	m, ok := mood.Get(moodKey)
	if !ok {
		m = mood.Surprise
	}

	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()

	res, err := b.pick(ctx, kind, m)
	components := []discordgo.MessageComponent{rerollRow(moodKey, kind)}

	edit := &discordgo.WebhookEdit{Components: &components}
	if err != nil {
		slog.Warn("pick failed", "mood", moodKey, "type", kind, "err", err)
		msg := "Couldn't reach the catalog right now. Try again in a sec."
		switch {
		case errors.Is(err, pick.ErrNoResults):
			msg = "Nothing matched that mood. Try a different one?"
		case errors.Is(err, errUnavailable):
			msg = "That type isn't set up yet."
		}
		edit.Content = &msg
		edit.Embeds = &[]*discordgo.MessageEmbed{}
	} else {
		empty := ""
		edit.Content = &empty
		edit.Embeds = &[]*discordgo.MessageEmbed{toEmbed(res, m)}
	}

	if _, err := b.s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Error("edit interaction response", "err", err)
	}
}

func (b *Bot) pick(ctx context.Context, kind string, m mood.Mood) (pick.Result, error) {
	k := pick.Kind(kind)
	if kind == kindAny {
		k = b.kinds[rand.IntN(len(b.kinds))]
	}
	src, ok := b.sources[k]
	if !ok {
		return pick.Result{}, errUnavailable
	}
	return src.Pick(ctx, m)
}

func rerollRow(moodKey, kind string) discordgo.ActionsRow {
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "Reroll",
			Style:    discordgo.PrimaryButton,
			CustomID: strings.Join([]string{rerollPrefix, moodKey, kind}, ":"),
			Emoji:    &discordgo.ComponentEmoji{Name: "🔄"},
		},
	}}
}

func toEmbed(r pick.Result, m mood.Mood) *discordgo.MessageEmbed {
	color := r.Color
	if color == 0 {
		color = defaultColors[r.Kind]
	}

	fields := make([]*discordgo.MessageEmbedField, 0, len(r.Fields))
	for _, f := range r.Fields {
		fields = append(fields, &discordgo.MessageEmbedField{Name: f.Name, Value: f.Value, Inline: true})
	}

	e := &discordgo.MessageEmbed{
		Title:       r.Title,
		URL:         r.URL,
		Description: r.Description,
		Color:       color,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%s %s · %s · via %s", m.Emoji, m.Label, r.Kind.Label(), r.Source),
		},
	}
	if r.ImageURL != "" {
		e.Image = &discordgo.MessageEmbedImage{URL: r.ImageURL}
	}
	return e
}
