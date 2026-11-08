package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

const (
	votePrefix    = "vote:"    // vote:<voteID>:<option index>
	voteEndPrefix = "voteend:" // voteend:<voteID>

	minVoteOptions     = 2
	maxVoteOptions     = 5 // one row of buttons
	defaultVoteOptions = 3
	defaultVoteTimer   = "2m"
	// The original interaction token, used to close a vote when its timer
	// fires, expires after 15 minutes. Untimed votes auto-close before that.
	untimedVoteLimit = 14 * time.Minute
	voteBuildTimeout = 30 * time.Second
)

var numberEmoji = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣"}

// voteTimers are the timer option choices. "none" means "until someone
// clicks End vote" (with a safety cap of untimedVoteLimit).
var voteTimers = []struct {
	Key, Label string
	D          time.Duration
}{
	{"30s", "30 seconds", 30 * time.Second},
	{"1m", "1 minute", time.Minute},
	{"2m", "2 minutes (default)", 2 * time.Minute},
	{"5m", "5 minutes", 5 * time.Minute},
	{"10m", "10 minutes", 10 * time.Minute},
	{"none", "No timer (end with the button)", 0},
}

func parseVoteTimer(key string) time.Duration {
	if key == "" {
		key = defaultVoteTimer
	}
	for _, t := range voteTimers {
		if t.Key == key {
			return t.D
		}
	}
	return parseVoteTimer(defaultVoteTimer)
}

// vote is one in-progress /vote. Votes live in memory only: a restart ends
// them, and their buttons then say the vote has ended.
type vote struct {
	mu sync.Mutex

	id      string
	scope   string
	req     request
	mood    mood.Mood
	options []pick.Result

	ballots map[string]int    // user ID -> option index
	names   map[string]string // user ID -> display name, for the voter list
	order   []string          // user IDs in the order they first voted

	endsAt      time.Time // zero when untimed
	timer       *time.Timer
	interaction *discordgo.Interaction // the /vote command, for timer-based edits
	ended       bool
}

// cast records a user's vote. Voting for the same option again takes the
// vote back; voting for another option switches to it.
func (v *vote) cast(userID, name string, option int) {
	if prev, ok := v.ballots[userID]; ok && prev == option {
		delete(v.ballots, userID)
		v.order = slices.DeleteFunc(v.order, func(id string) bool { return id == userID })
		return
	}
	if _, ok := v.ballots[userID]; !ok {
		v.order = append(v.order, userID)
	}
	v.ballots[userID] = option
	v.names[userID] = name
}

func (v *vote) counts() []int {
	c := make([]int, len(v.options))
	for _, opt := range v.ballots {
		if opt >= 0 && opt < len(c) {
			c[opt]++
		}
	}
	return c
}

// winner returns the winning option and how it was decided: "votes" (a clear
// winner), "tie" (random among the tied leaders), or "none" (nobody voted,
// random among all).
func winner(counts []int, intN func(int) int) (int, string) {
	best := 0
	for _, c := range counts {
		best = max(best, c)
	}
	var leaders []int
	for i, c := range counts {
		if c == best {
			leaders = append(leaders, i)
		}
	}
	switch {
	case best == 0:
		return intN(len(counts)), "none"
	case len(leaders) > 1:
		return leaders[intN(len(leaders))], "tie"
	}
	return leaders[0], "votes"
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// activeView renders the vote while it's open.
func (v *vote) activeView() (string, []*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	var content strings.Builder
	content.WriteString("🗳️ **Vote!** ")
	if v.endsAt.IsZero() {
		content.WriteString("Click **End vote** when everyone's in.")
	} else {
		// Discord renders this as a live countdown ("in 2 minutes").
		fmt.Fprintf(&content, "Voting ends <t:%d:R>.", v.endsAt.Unix())
	}
	if len(v.order) > 0 {
		names := make([]string, 0, len(v.order))
		for _, id := range v.order {
			names = append(names, escape(v.names[id]))
		}
		fmt.Fprintf(&content, "\n✅ %d voted: %s", len(v.order), strings.Join(names, ", "))
	}

	embeds := make([]*discordgo.MessageEmbed, 0, len(v.options))
	for n, r := range v.options {
		embeds = append(embeds, optionEmbed(n, r))
	}
	embeds[len(embeds)-1].Footer = &discordgo.MessageEmbedFooter{Text: v.footer()}

	counts := v.counts()
	buttons := make([]discordgo.MessageComponent, 0, len(v.options))
	for n, r := range v.options {
		buttons = append(buttons, discordgo.Button{
			Label:    fmt.Sprintf("%s · %d", truncate(r.Title, 40), counts[n]),
			Style:    discordgo.PrimaryButton,
			CustomID: fmt.Sprintf("%s%s:%d", votePrefix, v.id, n),
			Emoji:    &discordgo.ComponentEmoji{Name: numberEmoji[n]},
		})
	}
	end := discordgo.Button{
		Label:    "End vote",
		Style:    discordgo.DangerButton,
		CustomID: voteEndPrefix + v.id,
		Emoji:    &discordgo.ComponentEmoji{Name: "🏁"},
	}
	return content.String(), embeds, []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: buttons},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{end}},
	}
}

// finalView renders the result. intN breaks ties and is injectable for tests.
func (v *vote) finalView(intN func(int) int) (string, []*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	counts := v.counts()
	win, how := winner(counts, intN)
	w := v.options[win]

	var content string
	switch how {
	case "votes":
		content = fmt.Sprintf("🏆 **%s** wins with %s!", escape(w.Title), plural(counts[win], "vote"))
	case "tie":
		content = fmt.Sprintf("🏆 It's a tie! I broke it at random: **%s**.", escape(w.Title))
	default:
		content = fmt.Sprintf("🏆 Nobody voted, so I picked at random: **%s**.", escape(w.Title))
	}

	// Who voted for what, now that it's over.
	voters := make([][]string, len(v.options))
	for _, id := range v.order {
		opt := v.ballots[id]
		voters[opt] = append(voters[opt], escape(v.names[id]))
	}
	var lines []string
	for n, r := range v.options {
		line := fmt.Sprintf("%s **%s** · %s", numberEmoji[n], escape(truncate(r.Title, 60)), plural(counts[n], "vote"))
		if len(voters[n]) > 0 {
			line += " (" + strings.Join(voters[n], ", ") + ")"
		}
		if n == win {
			line += " 🏆"
		}
		lines = append(lines, line)
	}
	results := &discordgo.MessageEmbed{
		Title:       "Results",
		Description: strings.Join(lines, "\n"),
		Color:       0x95a5a6,
	}

	winEmbed := toEmbed(w, v.mood, v.req)
	saved := v.req.From == fromWatchlist

	// Winner: Seen it, Save, Trailer. There's no Reroll button, so Seen it
	// just marks the title without replacing the message.
	winRow := []discordgo.MessageComponent{
		discordgo.Button{Label: "Seen it", Style: discordgo.SecondaryButton, CustomID: seenPrefix + w.ID, Emoji: &discordgo.ComponentEmoji{Name: "👁️"}},
	}
	if saved {
		winRow = append(winRow, savedButton(w.ID))
	} else {
		winRow = append(winRow, discordgo.Button{Label: "Save", Style: discordgo.SecondaryButton, CustomID: savePrefix + w.ID, Emoji: &discordgo.ComponentEmoji{Name: "⭐"}})
	}
	if w.TrailerURL != "" {
		winRow = append(winRow, discordgo.Button{Label: "Trailer", Style: discordgo.LinkButton, URL: w.TrailerURL, Emoji: &discordgo.ComponentEmoji{Name: "▶️"}})
	}
	components := []discordgo.MessageComponent{discordgo.ActionsRow{Components: winRow}}

	// Runners-up can be saved for another night.
	if !saved {
		var others []discordgo.MessageComponent
		for n, r := range v.options {
			if n == win {
				continue
			}
			others = append(others, discordgo.Button{
				Label:    "Save " + numberEmoji[n],
				Style:    discordgo.SecondaryButton,
				CustomID: savePrefix + r.ID,
			})
		}
		components = append(components, discordgo.ActionsRow{Components: others})
	}

	return content, []*discordgo.MessageEmbed{winEmbed, results}, components
}

func (v *vote) footer() string {
	if v.req.From == fromWatchlist {
		return "⭐ From your watchlist"
	}
	parts := []string{v.mood.Emoji + " " + v.mood.Label}
	if v.req.Kind != kindAny && v.req.Kind != "" {
		parts = append(parts, pick.Kind(v.req.Kind).Label())
	}
	parts = append(parts, pick.Describe(v.req.Rating, v.req.Length, v.req.Era)...)
	return strings.Join(parts, " · ")
}

// optionEmbed is a compact card for one option while voting.
func optionEmbed(n int, r pick.Result) *discordgo.MessageEmbed {
	desc := truncate(r.Description, 220)
	if r.TrailerURL != "" {
		desc += "\n[▶️ Trailer](" + r.TrailerURL + ")"
	}
	var fields []*discordgo.MessageEmbedField
	for _, f := range r.Fields {
		if f.Name == "Genres" { // too long for a compact card
			continue
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: f.Name, Value: f.Value, Inline: true})
	}
	color := r.Color
	if color == 0 {
		color = defaultColors[r.Kind]
	}
	e := &discordgo.MessageEmbed{
		Title:       numberEmoji[n] + " " + r.Title,
		URL:         r.URL,
		Description: desc,
		Color:       color,
		Fields:      fields,
	}
	if r.ImageURL != "" {
		e.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: r.ImageURL}
	}
	return e
}

func newVoteID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Discord handlers ---

func (b *Bot) handleVoteCommand(i *discordgo.InteractionCreate, req request, count int, timerKey string) {
	if !b.ack(i, discordgo.InteractionResponseDeferredChannelMessageWithSource) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), voteBuildTimeout)
	defer cancel()

	m, ok := mood.Get(req.Mood)
	if !ok {
		m = mood.Surprise
	}
	scope := scopeOf(i)
	options, err := b.chooseN(ctx, scope, req, m, count)
	if err == nil && len(options) < minVoteOptions {
		err = errNotEnoughOptions
	}
	if err != nil {
		slog.Warn("build vote", "request", req, "err", err)
		msg := errorMessage(err, req)
		if errors.Is(err, errNotEnoughOptions) {
			msg = "Couldn't find at least 2 different options for that. Try a different mood or loosen the filters?"
		}
		b.s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	v := &vote{
		id: newVoteID(), scope: scope, req: req, mood: m, options: options,
		ballots: map[string]int{}, names: map[string]string{},
		interaction: i.Interaction,
	}
	d := parseVoteTimer(timerKey)
	limit := untimedVoteLimit
	if d > 0 {
		v.endsAt = time.Now().Add(d)
		limit = d
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	b.votesMu.Lock()
	b.votes[v.id] = v
	b.votesMu.Unlock()

	content, embeds, comps := v.activeView()
	if _, err := b.s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content, Embeds: &embeds, Components: &comps,
	}); err != nil {
		slog.Error("post vote", "err", err)
		b.dropVote(v.id)
		return
	}
	v.timer = time.AfterFunc(limit, func() { b.expireVote(v.id) })
}

var errNotEnoughOptions = errors.New("not enough distinct options")

// chooseN picks up to n distinct titles for a vote, skipping seen titles.
func (b *Bot) chooseN(ctx context.Context, scope string, req request, m mood.Mood, n int) ([]pick.Result, error) {
	if req.From == fromWatchlist {
		kind := pick.Kind(req.Kind)
		if req.Kind == kindAny {
			kind = ""
		}
		rs, err := b.store.RandomFromWatchlistN(ctx, scope, kind, n)
		if err == nil && len(rs) == 0 {
			err = errWatchlistEmpty
		}
		return rs, err
	}

	f := pick.ParseFilters(req.Rating, req.Length, req.Era)
	f.Exclude = map[string]bool{}
	if seen, err := b.store.SeenKeys(ctx, scope); err != nil {
		slog.Warn("load seen list; voting without it", "err", err)
	} else {
		f.Exclude = seen
	}

	var out []pick.Result
	for len(out) < n {
		res, err := b.pick(ctx, req.Kind, m, f)
		if err != nil {
			if len(out) >= minVoteOptions {
				break // use what we have
			}
			return out, err
		}
		f.Exclude[res.ID] = true // keep options distinct
		if err := b.store.SaveItem(ctx, res); err != nil {
			slog.Warn("cache item", "id", res.ID, "err", err)
		}
		out = append(out, res)
	}
	return out, nil
}

func (b *Bot) lookupVote(id string) *vote {
	b.votesMu.Lock()
	defer b.votesMu.Unlock()
	return b.votes[id]
}

func (b *Bot) dropVote(id string) {
	b.votesMu.Lock()
	defer b.votesMu.Unlock()
	delete(b.votes, id)
}

// replyEphemeral answers a component click with a message only the clicker sees.
func (b *Bot) replyEphemeral(i *discordgo.InteractionCreate, msg string) {
	err := b.s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: msg, Flags: discordgo.MessageFlagsEphemeral},
	})
	if err != nil {
		slog.Error("ephemeral reply", "err", err)
	}
}

// updateMessage answers a component click by rewriting the message it's on.
func (b *Bot) updateMessage(i *discordgo.InteractionCreate, content string, embeds []*discordgo.MessageEmbed, comps []discordgo.MessageComponent) {
	err := b.s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: content, Embeds: embeds, Components: comps},
	})
	if err != nil {
		slog.Error("update vote message", "err", err)
	}
}

func (b *Bot) handleBallot(i *discordgo.InteractionCreate, rest string) {
	id, nStr, ok := strings.Cut(rest, ":")
	n, err := strconv.Atoi(nStr)
	v := b.lookupVote(id)
	if !ok || err != nil || v == nil {
		b.replyEphemeral(i, "This vote has ended.")
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock() // held while responding so updates land in order
	if v.ended {
		b.replyEphemeral(i, "This vote has ended.")
		return
	}
	if n < 0 || n >= len(v.options) {
		return
	}
	v.cast(userOf(i).ID, displayName(i), n)
	content, embeds, comps := v.activeView()
	b.updateMessage(i, content, embeds, comps)
}

func (b *Bot) handleVoteEnd(i *discordgo.InteractionCreate, id string) {
	v := b.lookupVote(id)
	if v == nil {
		b.replyEphemeral(i, "This vote has already ended.")
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ended {
		b.replyEphemeral(i, "This vote has already ended.")
		return
	}
	b.finish(v)
	content, embeds, comps := v.finalView(mrand.IntN)
	b.updateMessage(i, content, embeds, comps)
}

// expireVote closes a vote when its timer fires.
func (b *Bot) expireVote(id string) {
	v := b.lookupVote(id)
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ended {
		return
	}
	b.finish(v)
	content, embeds, comps := v.finalView(mrand.IntN)
	if _, err := b.s.InteractionResponseEdit(v.interaction, &discordgo.WebhookEdit{
		Content: &content, Embeds: &embeds, Components: &comps,
	}); err != nil {
		slog.Error("close expired vote", "id", id, "err", err)
	}
}

// finish marks a vote ended and forgets it. Caller holds v.mu.
func (b *Bot) finish(v *vote) {
	v.ended = true
	if v.timer != nil {
		v.timer.Stop()
	}
	b.dropVote(v.id)
}

// voteOptions is the /vote command's options: count and timer, then the
// same filters as /pick.
func (b *Bot) voteOptions(pickOpts []*discordgo.ApplicationCommandOption) []*discordgo.ApplicationCommandOption {
	minOpts := float64(minVoteOptions)
	timerChoices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(voteTimers))
	for _, t := range voteTimers {
		timerChoices = append(timerChoices, &discordgo.ApplicationCommandOptionChoice{Name: t.Label, Value: t.Key})
	}
	opts := []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionInteger,
			Name:        "count",
			Description: fmt.Sprintf("How many options to vote between (default %d)", defaultVoteOptions),
			MinValue:    &minOpts,
			MaxValue:    maxVoteOptions,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "timer",
			Description: "How long voting stays open",
			Choices:     timerChoices,
		},
	}
	return append(opts, pickOpts...)
}
