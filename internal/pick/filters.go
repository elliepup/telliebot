package pick

import (
	"strconv"
	"time"
)

// DefaultMinRating applies when no rating filter is chosen.
const DefaultMinRating = 6.5

// Range is an inclusive integer range. Zero on either end means unbounded.
type Range struct {
	Min, Max int
}

// Filters narrows a pick beyond the mood. The zero value of each range means
// "no constraint".
type Filters struct {
	MinRating float64 // 0–10 scale

	MovieMinutes       Range // movie runtime
	AnimeEpisodes      Range // anime episode count
	ShowEpisodeMinutes Range // TV episode runtime (TMDB can't filter by season count)

	Years Range // release / first-air year
}

// Choice is a selectable filter value shown in Discord.
type Choice struct {
	Key   string // value sent by Discord and stored in reroll buttons
	Label string // shown in the option dropdown
	Short string // shown in the embed footer
}

// RatingChoices are the min_rating options.
var RatingChoices = []Choice{
	{Key: "6", Label: "⭐ 6+", Short: "⭐ 6+"},
	{Key: "7", Label: "⭐ 7+", Short: "⭐ 7+"},
	{Key: "7.5", Label: "⭐ 7.5+", Short: "⭐ 7.5+"},
	{Key: "8", Label: "⭐ 8+", Short: "⭐ 8+"},
	{Key: "8.5", Label: "⭐ 8.5+ (only the best)", Short: "⭐ 8.5+"},
}

// LengthChoices are the length options. Each one means something different
// per type, and the label says how.
var LengthChoices = []Choice{
	{Key: "short", Label: "Short · movies ≤100 min, anime ≤13 eps, shows ≤30 min/ep", Short: "Short"},
	{Key: "medium", Label: "Medium · movies 100–140 min, anime 14–26 eps, shows 30–50 min/ep", Short: "Medium"},
	{Key: "long", Label: "Long · movies 140+ min, anime 27+ eps, shows 50+ min/ep", Short: "Long"},
}

// EraChoices are the era options.
var EraChoices = []Choice{
	{Key: "classic", Label: "Classic · before 2000", Short: "Classic"},
	{Key: "2000s", Label: "2000s", Short: "2000s"},
	{Key: "2010s", Label: "2010s", Short: "2010s"},
	{Key: "recent", Label: "Recent · last 3 years", Short: "Recent"},
}

// ParseFilters turns option keys into filters. Unknown or empty keys mean
// "no constraint" (or the default rating).
func ParseFilters(rating, length, era string) Filters {
	f := Filters{MinRating: DefaultMinRating}

	if r, err := strconv.ParseFloat(rating, 64); err == nil && r > 0 && r <= 10 {
		f.MinRating = r
	}

	switch length {
	case "short":
		f.MovieMinutes = Range{Max: 100}
		f.AnimeEpisodes = Range{Max: 13}
		f.ShowEpisodeMinutes = Range{Max: 30}
	case "medium":
		f.MovieMinutes = Range{Min: 101, Max: 140}
		f.AnimeEpisodes = Range{Min: 14, Max: 26}
		f.ShowEpisodeMinutes = Range{Min: 31, Max: 50}
	case "long":
		f.MovieMinutes = Range{Min: 141}
		f.AnimeEpisodes = Range{Min: 27}
		f.ShowEpisodeMinutes = Range{Min: 51}
	}

	switch era {
	case "classic":
		f.Years = Range{Max: 1999}
	case "2000s":
		f.Years = Range{Min: 2000, Max: 2009}
	case "2010s":
		f.Years = Range{Min: 2010, Max: 2019}
	case "recent":
		// Computed per pick so a long-running bot doesn't drift across years.
		f.Years = Range{Min: time.Now().Year() - 2}
	}

	return f
}

// Describe returns short labels for the chosen filter keys, for display.
func Describe(rating, length, era string) []string {
	var out []string
	for _, c := range []struct {
		key     string
		choices []Choice
	}{{rating, RatingChoices}, {length, LengthChoices}, {era, EraChoices}} {
		for _, ch := range c.choices {
			if ch.Key == c.key {
				out = append(out, ch.Short)
			}
		}
	}
	return out
}
