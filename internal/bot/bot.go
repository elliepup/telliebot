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
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
	"github.com/elliepup/telliebot/internal/storage"
)

const (
	kindAny       = "any"
	fromWatchlist = "watchlist"
	pickTimeout   = 15 * time.Second
)

var (
	errUnavailable    = errors.New("source not configured")
	errWatchlistEmpty = errors.New("watchlist empty")
)

var defaultColors = map[pick.Kind]int{
	pick.Anime: 0x02a9ff, // AniList blue
	pick.Movie: 0xf5c518,
	pick.Show:  0x9b59b6,
}

// request is everything the user asked for in one /pick. It round-trips
// through the reroll button's custom ID, so values must not contain ':'.
type request struct {
	Mood, Kind, Rating, Length, Era, From string
}

func (r request) customID() string {
	return strings.Join([]string{rerollPrefix, r.Mood, r.Kind, r.Rating, r.Length, r.Era, r.From}, ":")
}

// parseCustomID reads a reroll button ID. Older 3- and 6-part forms are
// still accepted so buttons on existing messages keep working.
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
	case 7:
		return request{Mood: p[1], Kind: p[2], Rating: p[3], Length: p[4], Era: p[5], From: p[6]}, true
	}
	return request{}, false
}

// Bot is a running Discord session.
type Bot struct {
	s       *discordgo.Session
	guildID string
	sources map[pick.Kind]pick.Source
	kinds   []pick.Kind // configured kinds, sorted, for stable choices
	store   *storage.Store

	votesMu sync.Mutex
	votes   map[string]*vote // open votes by ID
}

// New creates a bot. Call Open to connect.
func New(token, guildID string, sources map[pick.Kind]pick.Source, store *storage.Store) (*Bot, error) {
	if len(sources) == 0 {
		return nil, errors.New("no title sources configured")
	}
	if store == nil {
		return nil, errors.New("no store configured")
	}
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsGuilds

	b := &Bot{s: s, guildID: guildID, sources: sources, store: store, votes: map[string]*vote{}}
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

	// The watchlist can hold any kind, even ones not configured right now.
	allKinds := []*discordgo.ApplicationCommandOptionChoice{
		{Name: pick.Anime.Label(), Value: string(pick.Anime)},
		{Name: pick.Movie.Label(), Value: string(pick.Movie)},
		{Name: pick.Show.Label(), Value: string(pick.Show)},
	}

	str := discordgo.ApplicationCommandOptionString
	pickOpts := []*discordgo.ApplicationCommandOption{
		{Type: str, Name: "mood", Description: "How are you feeling?", Choices: moodChoices},
		{Type: str, Name: "type", Description: "What kind of thing to watch", Choices: typeChoices},
		{Type: str, Name: "min_rating", Description: fmt.Sprintf("Minimum rating out of 10 (default %.1f)", pick.DefaultMinRating), Choices: toOptionChoices(pick.RatingChoices)},
		{Type: str, Name: "length", Description: "How much time you've got", Choices: toOptionChoices(pick.LengthChoices)},
		{Type: str, Name: "era", Description: "When it came out", Choices: toOptionChoices(pick.EraChoices)},
		{Type: str, Name: "from", Description: "Where to pick from", Choices: []*discordgo.ApplicationCommandOptionChoice{
			{Name: "🌐 Everything (default)", Value: "everything"},
			{Name: "⭐ Our watchlist (ignores mood and filters)", Value: fromWatchlist},
		}},
	}
	return []*discordgo.ApplicationCommand{
		{
			Name:        "pick",
			Description: "Pick something random to watch",
			Options:     pickOpts,
		},
		{
			Name:        "vote",
			Description: "Vote between a few random picks",
			Options:     b.voteOptions(pickOpts),
		},
		{
			Name:        "watchlist",
			Description: "Show your saved watchlist",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: str, Name: "type", Description: "Only show one type", Choices: allKinds},
			},
		},
	}
}

func (b *Bot) onInteraction(_ *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		data := i.ApplicationCommandData()
		switch data.Name {
		case "pick":
			b.respond(i, discordgo.InteractionResponseDeferredChannelMessageWithSource, parseRequest(data.Options))
		case "vote":
			count, timer := defaultVoteOptions, ""
			for _, o := range data.Options {
				switch o.Name {
				case "count":
					count = min(max(int(o.IntValue()), minVoteOptions), maxVoteOptions)
				case "timer":
					timer = o.StringValue()
				}
			}
			b.handleVoteCommand(i, parseRequest(data.Options), count, timer)
		case "watchlist":
			kind := ""
			for _, o := range data.Options {
				if o.Name == "type" {
					kind = o.StringValue()
				}
			}
			b.handleWatchlist(i, kind)
		}

	case discordgo.InteractionMessageComponent:
		b.onComponent(i)
	}
}

// parseRequest reads the options shared by /pick and /vote. Unknown option
// names (like /vote's count and timer) are ignored.
func parseRequest(opts []*discordgo.ApplicationCommandInteractionDataOption) request {
	req := request{Mood: mood.Surprise.Key, Kind: kindAny}
	for _, o := range opts {
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
		case "from":
			if o.StringValue() == fromWatchlist {
				req.From = fromWatchlist
			}
		}
	}
	return req
}

// ack acknowledges an interaction right away; Discord allows only 3 seconds.
func (b *Bot) ack(i *discordgo.InteractionCreate, t discordgo.InteractionResponseType) bool {
	if err := b.s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: t}); err != nil {
		slog.Error("ack interaction", "err", err)
		return false
	}
	return true
}

// respond acknowledges the interaction, then fetches a pick and edits the
// response with it.
func (b *Bot) respond(i *discordgo.InteractionCreate, ack discordgo.InteractionResponseType, req request) {
	if !b.ack(i, ack) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout)
	defer cancel()
	b.pickAndRender(ctx, i, req, "")
}

// pickAndRender picks a title for req and edits the (already acknowledged)
// interaction response with it. note, if set, is shown above the pick.
func (b *Bot) pickAndRender(ctx context.Context, i *discordgo.InteractionCreate, req request, note string) {
	scope := scopeOf(i)
	m, ok := mood.Get(req.Mood)
	if !ok {
		m = mood.Surprise
	}

	res, err := b.choose(ctx, scope, req, m)

	edit := &discordgo.WebhookEdit{}
	var components []discordgo.MessageComponent
	if err != nil {
		slog.Warn("pick failed", "request", req, "err", err)
		msg := errorMessage(err, req)
		if note != "" {
			msg = note + "\n" + msg
		}
		edit.Content = &msg
		edit.Embeds = &[]*discordgo.MessageEmbed{}
		components = []discordgo.MessageComponent{buttonRow(req, nil, false)}
	} else {
		saved := req.From == fromWatchlist
		if !saved {
			if saved, err = b.store.InWatchlist(ctx, scope, res.ID); err != nil {
				slog.Warn("check watchlist", "err", err)
			}
		}
		edit.Content = &note
		edit.Embeds = &[]*discordgo.MessageEmbed{toEmbed(res, m, req)}
		components = []discordgo.MessageComponent{buttonRow(req, &res, saved)}
	}
	edit.Components = &components

	if _, err := b.s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Error("edit interaction response", "err", err)
	}
}

// choose returns a title for req: a random saved one when picking from the
// watchlist, otherwise a fresh pick from the sources that skips seen titles.
func (b *Bot) choose(ctx context.Context, scope string, req request, m mood.Mood) (pick.Result, error) {
	if req.From == fromWatchlist {
		kind := pick.Kind(req.Kind)
		if req.Kind == kindAny {
			kind = ""
		}
		r, ok, err := b.store.RandomFromWatchlist(ctx, scope, kind)
		if err != nil {
			return r, err
		}
		if !ok {
			return r, errWatchlistEmpty
		}
		return r, nil
	}

	f := pick.ParseFilters(req.Rating, req.Length, req.Era)
	if seen, err := b.store.SeenKeys(ctx, scope); err != nil {
		slog.Warn("load seen list; picking without it", "err", err)
	} else {
		f.Exclude = seen
	}

	res, err := b.pick(ctx, req.Kind, m, f)
	if err != nil {
		return res, err
	}
	// Cache the title so Seen it / Save can refer to it by ID.
	if err := b.store.SaveItem(ctx, res); err != nil {
		slog.Warn("cache item", "id", res.ID, "err", err)
	}
	return res, nil
}

func errorMessage(err error, req request) string {
	switch {
	case errors.Is(err, errWatchlistEmpty):
		if req.Kind != kindAny && req.Kind != "" {
			return fmt.Sprintf("No %s on your watchlist yet. Hit ⭐ Save on a pick to add one.", strings.ToLower(pick.Kind(req.Kind).Label())+"s")
		}
		return "Your watchlist is empty. Hit ⭐ Save on a pick to add something."
	case errors.Is(err, pick.ErrNoResults):
		return "Nothing matched that (or you've seen it all!). Try a different mood or loosen the filters?"
	case errors.Is(err, errUnavailable):
		return "That type isn't set up yet."
	}
	return "Couldn't reach the catalog right now. Try again in a sec."
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

func toEmbed(r pick.Result, m mood.Mood, req request) *discordgo.MessageEmbed {
	color := r.Color
	if color == 0 {
		color = defaultColors[r.Kind]
	}

	fields := make([]*discordgo.MessageEmbedField, 0, len(r.Fields))
	for _, f := range r.Fields {
		fields = append(fields, &discordgo.MessageEmbedField{Name: f.Name, Value: f.Value, Inline: true})
	}

	var footer []string
	if req.From == fromWatchlist {
		footer = []string{"⭐ From your watchlist", r.Kind.Label()}
	} else {
		footer = []string{m.Emoji + " " + m.Label, r.Kind.Label()}
		footer = append(footer, pick.Describe(req.Rating, req.Length, req.Era)...)
	}
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

// scopeOf returns whose lists an interaction uses: the server's shared list,
// or the user's own in DMs.
func scopeOf(i *discordgo.InteractionCreate) string {
	if i.GuildID != "" {
		return "guild:" + i.GuildID
	}
	if u := userOf(i); u != nil {
		return "user:" + u.ID
	}
	return "unknown"
}

func userOf(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

func displayName(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.Nick != "" {
		return i.Member.Nick
	}
	u := userOf(i)
	switch {
	case u == nil:
		return "Someone"
	case u.GlobalName != "":
		return u.GlobalName
	}
	return u.Username
}

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, `*`, `\*`, `_`, `\_`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`, `[`, `\[`, `]`, `\]`,
)

// escape makes text safe to embed in Discord markdown.
func escape(s string) string { return markdownEscaper.Replace(s) }
