// Package tmdb picks random movies and shows from The Movie Database.
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

const (
	baseURL   = "https://api.themoviedb.org/3"
	imageBase = "https://image.tmdb.org/t/p/w500"
	siteBase  = "https://www.themoviedb.org"
	// maxPage limits picks to the top maxPage*20 most popular matches.
	maxPage = 25
	// showGenresExcluded: Kids, News, Reality, Soap, Talk (TMDB TV genre IDs).
	showGenresExcluded = "10762,10763,10764,10766,10767"
	// animationGenre is Animation in both TMDB's movie and TV genre lists.
	animationGenre = 16
)

// Client talks to TMDB.
type Client struct {
	key  string
	http *http.Client
}

// New returns a TMDB client. key may be a v3 API key or a v4 read access
// token (a JWT starting with "eyJ").
func New(key string) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 10 * time.Second}}
}

// Movies returns a source that picks movies.
func (c *Client) Movies() pick.Source { return source{c, pick.Movie} }

// Shows returns a source that picks TV shows.
func (c *Client) Shows() pick.Source { return source{c, pick.Show} }

type source struct {
	c    *Client
	kind pick.Kind
}

// itemID is the stable Result ID for a TMDB title. Movie and TV IDs are
// separate namespaces in TMDB, so the segment is part of the key.
func itemID(kind pick.Kind, id int) string {
	if kind == pick.Show {
		return "tmdb:tv:" + strconv.Itoa(id)
	}
	return "tmdb:movie:" + strconv.Itoa(id)
}

// parseItemID extracts the numeric TMDB ID from a Result ID of this kind.
func parseItemID(kind pick.Kind, key string) (int, bool) {
	prefix := strings.TrimSuffix(itemID(kind, 0), "0")
	s, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

// segment is the URL path segment TMDB uses for a kind.
func segment(kind pick.Kind) string {
	if kind == pick.Show {
		return "tv"
	}
	return "movie"
}

func (s source) Pick(ctx context.Context, m mood.Mood, f pick.Filters) (pick.Result, error) {
	return s.c.pick(ctx, s.kind, m, f)
}

func (s source) Similar(ctx context.Context, id string, f pick.Filters) (pick.Result, error) {
	return s.c.similar(ctx, s.kind, id, f)
}

type result struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"` // movies
	Name         string  `json:"name"`  // shows
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	VoteAverage  float64 `json:"vote_average"`
	ReleaseDate  string  `json:"release_date"`   // movies
	FirstAirDate string  `json:"first_air_date"` // shows

	OriginalLanguage string `json:"original_language"`
	GenreIDs         []int  `json:"genre_ids"`
	VoteCount        int    `json:"vote_count"`
	Adult            bool   `json:"adult"`
}

// isAnime reports whether a TMDB title is anime: Japanese-language
// animation. The Anime type already covers these through AniList, so they're
// skipped here. Other animation (Arcane, BoJack, Pixar) is kept.
func isAnime(r result) bool {
	return r.OriginalLanguage == "ja" && slices.Contains(r.GenreIDs, animationGenre)
}

type discoverResponse struct {
	TotalPages int      `json:"total_pages"`
	Results    []result `json:"results"`
}

func (c *Client) pick(ctx context.Context, kind pick.Kind, m mood.Mood, f pick.Filters) (pick.Result, error) {
	path, q := "/discover/movie", query(kind, m, f)
	if kind == pick.Show {
		path = "/discover/tv"
	}

	first, err := c.discover(ctx, path, q, 1)
	if err != nil {
		return pick.Result{}, err
	}
	fetch := func(ctx context.Context, p int) ([]result, error) {
		pg, err := c.discover(ctx, path, q, p)
		return pg.Results, err
	}
	// TMDB can't exclude IDs or combine "Animation and Japanese" in one
	// without_* parameter, so seen titles and anime are skipped locally.
	// RandomFromPages tries other pages if a page has nothing left.
	keep := func(r result) bool { return !f.Exclude[itemID(kind, r.ID)] && !isAnime(r) }

	chosen, err := pick.RandomFromPages(ctx, min(first.TotalPages, maxPage), first.Results, fetch, keep)
	if err != nil {
		return pick.Result{}, err
	}
	res := toResult(kind, chosen)
	res.TrailerURL = c.trailer(ctx, kind, chosen)
	return res, nil
}

const (
	// recPages caps how many pages of recommendations to choose from. TMDB
	// orders them by relevance, so later pages drift away from the title.
	recPages = 2
	// minRecVotes keeps a handful of votes from passing the rating filter.
	// Discover uses 200; recommendations are fewer, so this is lower.
	minRecVotes = 50
)

// similar picks a random recommendation for key (a Result ID of kind).
func (c *Client) similar(ctx context.Context, kind pick.Kind, key string, f pick.Filters) (pick.Result, error) {
	n, ok := parseItemID(kind, key)
	if !ok {
		return pick.Result{}, fmt.Errorf("tmdb: not a %s ID: %q", kind, key)
	}
	path := fmt.Sprintf("/%s/%d/recommendations", segment(kind), n)

	first, err := c.discover(ctx, path, url.Values{}, 1)
	if err != nil {
		return pick.Result{}, err
	}
	fetch := func(ctx context.Context, p int) ([]result, error) {
		pg, err := c.discover(ctx, path, url.Values{}, p)
		return pg.Results, err
	}
	keep := func(r result) bool { return keepSimilar(kind, r, f) }

	chosen, err := pick.RandomFromPages(ctx, min(first.TotalPages, recPages), first.Results, fetch, keep)
	if err != nil {
		return pick.Result{}, err
	}
	res := toResult(kind, chosen)
	res.TrailerURL = c.trailer(ctx, kind, chosen)
	return res, nil
}

// keepSimilar applies the same rules as discover picks to a recommendation,
// which TMDB returns unfiltered.
func keepSimilar(kind pick.Kind, r result, f pick.Filters) bool {
	if r.Adult || isAnime(r) || f.Exclude[itemID(kind, r.ID)] {
		return false
	}
	if r.VoteCount < minRecVotes || r.VoteAverage < f.MinRating {
		return false
	}
	if kind == pick.Show {
		excluded := strings.Split(showGenresExcluded, ",")
		for _, g := range r.GenreIDs {
			if slices.Contains(excluded, strconv.Itoa(g)) {
				return false
			}
		}
	}
	return true
}

// query builds the discover parameters (minus page and auth) for a kind,
// mood, and filters. TMDB's .gte/.lte parameters are inclusive.
func query(kind pick.Kind, m mood.Mood, f pick.Filters) url.Values {
	q := url.Values{}
	q.Set("include_adult", "false")
	q.Set("sort_by", "popularity.desc")
	q.Set("vote_count.gte", "200")
	q.Set("vote_average.gte", strconv.FormatFloat(f.MinRating, 'f', -1, 64))

	genres, runtime, dateParam := m.MovieGenres, f.MovieMinutes, "primary_release_date"
	if kind == pick.Show {
		genres, runtime, dateParam = m.ShowGenres, f.ShowEpisodeMinutes, "first_air_date"
	}

	if len(genres) > 0 {
		ids := make([]string, len(genres))
		for i, g := range genres {
			ids[i] = strconv.Itoa(g)
		}
		q.Set("with_genres", strings.Join(ids, "|")) // | = OR
	}
	if kind == pick.Show {
		// TMDB's TV catalog includes daily news, talk, reality, and soap
		// programs, which are never what you want from a random pick.
		q.Set("without_genres", showGenresExcluded)
	}
	if runtime.Min > 0 {
		q.Set("with_runtime.gte", strconv.Itoa(runtime.Min))
	}
	if runtime.Max > 0 {
		q.Set("with_runtime.lte", strconv.Itoa(runtime.Max))
	}
	if f.Years.Min > 0 {
		q.Set(dateParam+".gte", fmt.Sprintf("%04d-01-01", f.Years.Min))
	}
	if f.Years.Max > 0 {
		q.Set(dateParam+".lte", fmt.Sprintf("%04d-12-31", f.Years.Max))
	}
	return q
}

func (c *Client) discover(ctx context.Context, path string, base url.Values, page int) (discoverResponse, error) {
	q := url.Values{}
	for k, v := range base {
		q[k] = v
	}
	q.Set("page", strconv.Itoa(page))

	var out discoverResponse
	err := c.get(ctx, path, q, &out)
	return out, err
}

type video struct {
	Key      string `json:"key"`
	Site     string `json:"site"`
	Type     string `json:"type"`
	Official bool   `json:"official"`
	Language string `json:"iso_639_1"`
}

// trailer looks up the best YouTube trailer for a title. It returns "" when
// there is none or the lookup fails, since a missing trailer shouldn't sink
// the whole pick.
func (c *Client) trailer(ctx context.Context, kind pick.Kind, r result) string {
	// Without include_video_language TMDB only returns en-US videos, which
	// leaves most non-English titles without a trailer.
	langs := "en,null"
	if r.OriginalLanguage != "" && r.OriginalLanguage != "en" {
		langs = "en," + r.OriginalLanguage + ",null"
	}
	q := url.Values{}
	q.Set("include_video_language", langs)

	var out struct {
		Results []video `json:"results"`
	}
	if err := c.get(ctx, fmt.Sprintf("/%s/%d/videos", segment(kind), r.ID), q, &out); err != nil {
		slog.Warn("tmdb trailer lookup failed", "id", r.ID, "kind", kind, "err", err)
		return ""
	}
	if v, ok := bestVideo(out.Results); ok {
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(v.Key)
	}
	return ""
}

// bestVideo prefers official trailers, then any trailer, then teasers, and
// English over other languages within each tier. Only YouTube videos count,
// because the URL format is built for YouTube.
func bestVideo(vs []video) (video, bool) {
	score := func(v video) int {
		s := 0
		switch v.Type {
		case "Trailer":
			s += 100
		case "Teaser":
			s += 50
		default:
			return -1
		}
		if v.Official {
			s += 10
		}
		if v.Language == "en" {
			s += 1
		}
		return s
	}

	best, bestScore := video{}, -1
	for _, v := range vs {
		if v.Site != "YouTube" || v.Key == "" {
			continue
		}
		if s := score(v); s > bestScore {
			best, bestScore = v, s
		}
	}
	return best, bestScore >= 0
}

// get performs an authenticated GET and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	bearer := strings.HasPrefix(c.key, "eyJ")
	if !bearer {
		q.Set("api_key", c.key)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Strip the URL from the error so a v3 api_key never ends up in logs.
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return fmt.Errorf("tmdb request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("tmdb decode: %w", err)
	}
	return nil
}

func toResult(kind pick.Kind, r result) pick.Result {
	title, date := r.Title, r.ReleaseDate
	if kind == pick.Show {
		title, date = r.Name, r.FirstAirDate
	}

	var fields []pick.Field
	if r.VoteAverage > 0 {
		fields = append(fields, pick.Field{Name: "Rating", Value: fmt.Sprintf("%.1f/10", r.VoteAverage)})
	}
	if len(date) >= 4 {
		fields = append(fields, pick.Field{Name: "Year", Value: date[:4]})
	}

	res := pick.Result{
		ID:          itemID(kind, r.ID),
		Kind:        kind,
		Title:       title,
		URL:         fmt.Sprintf("%s/%s/%d", siteBase, segment(kind), r.ID),
		Description: pick.CleanText(r.Overview, 400),
		Fields:      fields,
		Source:      "TMDB",
	}
	if r.PosterPath != "" {
		res.ImageURL = imageBase + r.PosterPath
	}
	return res
}
