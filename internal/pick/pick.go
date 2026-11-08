// Package pick defines the shared types every title source returns.
package pick

import (
	"context"
	"errors"
	"html"
	"regexp"
	"strings"

	"github.com/elliepup/telliebot/internal/mood"
)

// Kind is the type of thing to watch.
type Kind string

const (
	Anime Kind = "anime"
	Movie Kind = "movie"
	Show  Kind = "show"
)

// Label returns a display name for the kind.
func (k Kind) Label() string {
	switch k {
	case Anime:
		return "Anime"
	case Movie:
		return "Movie"
	case Show:
		return "Show"
	}
	return string(k)
}

// ErrNoResults means nothing matched the filters.
var ErrNoResults = errors.New("no titles matched")

// Field is a short labeled fact shown on the result.
type Field struct {
	Name  string
	Value string
}

// Result is a single randomly picked title.
type Result struct {
	Kind        Kind
	Title       string
	URL         string
	Description string
	ImageURL    string
	Color       int // 0 means use the default color for the kind
	Fields      []Field
	Source      string // e.g. "AniList"
}

// Source picks a random title matching a mood and filters.
type Source interface {
	Pick(ctx context.Context, m mood.Mood, f Filters) (Result, error)
}

var (
	tagRE        = regexp.MustCompile(`<[^>]*>`)
	blankLinesRE = regexp.MustCompile(`\n{3,}`)
)

// CleanText strips HTML tags, unescapes entities, and truncates to max runes.
func CleanText(s string, max int) string {
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = blankLinesRE.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}
