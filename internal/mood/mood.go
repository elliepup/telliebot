// Package mood maps how you're feeling to genre filters for each source.
package mood

// Mood describes a vibe and the genres that fit it on each source.
// Genre lists are OR'd: a title matching any one of them qualifies.
type Mood struct {
	Key   string
	Label string
	Emoji string

	// AnimeGenres are AniList genre names.
	AnimeGenres []string
	// MovieGenres are TMDB movie genre IDs.
	MovieGenres []int
	// ShowGenres are TMDB TV genre IDs (TV uses a different ID set than movies).
	ShowGenres []int
}

// Surprise applies no genre filter.
var Surprise = Mood{Key: "surprise", Label: "Surprise me", Emoji: "🎲"}

// All lists every mood in the order shown in Discord.
var All = []Mood{
	Surprise,
	{
		Key: "cozy", Label: "Cozy", Emoji: "🛋️",
		AnimeGenres: []string{"Slice of Life"},
		MovieGenres: []int{10751, 16}, // Family, Animation
		ShowGenres:  []int{10751, 35}, // Family, Comedy
	},
	{
		Key: "funny", Label: "Funny", Emoji: "😂",
		AnimeGenres: []string{"Comedy"},
		MovieGenres: []int{35},
		ShowGenres:  []int{35},
	},
	{
		Key: "hype", Label: "Hype", Emoji: "🔥",
		AnimeGenres: []string{"Action", "Sports"},
		MovieGenres: []int{28, 12}, // Action, Adventure
		ShowGenres:  []int{10759},  // Action & Adventure
	},
	{
		Key: "emotional", Label: "Emotional", Emoji: "😭",
		AnimeGenres: []string{"Drama"},
		MovieGenres: []int{18},
		ShowGenres:  []int{18},
	},
	{
		Key: "romantic", Label: "Romantic", Emoji: "💕",
		AnimeGenres: []string{"Romance"},
		MovieGenres: []int{10749},
		ShowGenres:  []int{18}, // TMDB TV has no romance genre
	},
	{
		Key: "spooky", Label: "Spooky", Emoji: "👻",
		AnimeGenres: []string{"Horror", "Supernatural"},
		MovieGenres: []int{27},   // Horror
		ShowGenres:  []int{9648}, // Mystery (TMDB TV has no horror genre)
	},
	{
		Key: "mindbending", Label: "Mind-bending", Emoji: "🌀",
		AnimeGenres: []string{"Psychological", "Mystery"},
		MovieGenres: []int{9648, 878},   // Mystery, Science Fiction
		ShowGenres:  []int{9648, 10765}, // Mystery, Sci-Fi & Fantasy
	},
	{
		Key: "epic", Label: "Epic adventure", Emoji: "🗺️",
		AnimeGenres: []string{"Fantasy", "Adventure"},
		MovieGenres: []int{14, 12},       // Fantasy, Adventure
		ShowGenres:  []int{10765, 10759}, // Sci-Fi & Fantasy, Action & Adventure
	},
	{
		Key: "tense", Label: "Tense", Emoji: "😬",
		AnimeGenres: []string{"Thriller"},
		MovieGenres: []int{53, 80}, // Thriller, Crime
		ShowGenres:  []int{80},     // Crime
	},
}

// Get looks up a mood by key.
func Get(key string) (Mood, bool) {
	for _, m := range All {
		if m.Key == key {
			return m, true
		}
	}
	return Mood{}, false
}
