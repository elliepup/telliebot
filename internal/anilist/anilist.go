// Package anilist picks random anime from the AniList GraphQL API.
// No API key is required.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

const (
	endpoint = "https://graphql.anilist.co"
	perPage  = 25
	// maxPage limits picks to the top maxPage*perPage most popular matches,
	// which keeps quality up while still giving plenty of variety.
	maxPage = 20
)

// mediaFields is the selection decoded into media, shared by both queries.
const mediaFields = `
      id
      title { romaji english }
      description(asHtml: false)
      siteUrl
      coverImage { extraLarge color }
      averageScore
      episodes
      format
      seasonYear
      genres
      isAdult
      trailer { id site }`

// Null variables are ignored by AniList, so unset filters simply drop out.
// The *_greater / *_lesser arguments are exclusive.
const query = `query ($page: Int, $perPage: Int, $genres: [String], $minScore: Int,
                      $epGreater: Int, $epLesser: Int,
                      $startGreater: FuzzyDateInt, $startLesser: FuzzyDateInt,
                      $idNotIn: [Int]) {
  Page(page: $page, perPage: $perPage) {
    pageInfo { lastPage hasNextPage }
    media(type: ANIME, isAdult: false, genre_in: $genres, format_in: [TV, MOVIE, ONA],
          id_not_in: $idNotIn,
          averageScore_greater: $minScore,
          episodes_greater: $epGreater, episodes_lesser: $epLesser,
          startDate_greater: $startGreater, startDate_lesser: $startLesser,
          sort: POPULARITY_DESC) {` + mediaFields + `
    }
  }
}`

// recsQuery fetches the community's top-rated recommendations for a title.
// Only the first page is used: further down the list they get weak.
const recsQuery = `query ($id: Int, $perPage: Int) {
  Media(id: $id, type: ANIME) {
    recommendations(page: 1, perPage: $perPage, sort: [RATING_DESC]) {
      nodes {
        rating
        mediaRecommendation {` + mediaFields + `
        }
      }
    }
  }
}`

// pickFormats are the formats random picks use. Recommendations are held
// to the same set so they don't surface OVAs, specials, or music videos.
var pickFormats = map[string]bool{"TV": true, "MOVIE": true, "ONA": true}

// Client talks to AniList.
type Client struct {
	http     *http.Client
	endpoint string
	// firstPages caches page 1 per query (minus the seen list). Every pick
	// needs page 1 to learn lastPage, so this halves requests for rerolls
	// and votes, which matters when AniList limits us to 30 requests/min.
	firstPages *pick.Cache[cachedPage]
}

// New returns an AniList client.
func New() *Client {
	return &Client{
		http:       &http.Client{Timeout: 10 * time.Second},
		endpoint:   endpoint,
		firstPages: pick.NewCache[cachedPage](pick.DefaultCacheSize, pick.DefaultCacheTTL),
	}
}

type media struct {
	ID    int `json:"id"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
	} `json:"title"`
	Description string `json:"description"`
	SiteURL     string `json:"siteUrl"`
	CoverImage  struct {
		ExtraLarge string `json:"extraLarge"`
		Color      string `json:"color"`
	} `json:"coverImage"`
	AverageScore int      `json:"averageScore"`
	Episodes     int      `json:"episodes"`
	Format       string   `json:"format"`
	SeasonYear   int      `json:"seasonYear"`
	Genres       []string `json:"genres"`
	IsAdult      bool     `json:"isAdult"`
	Trailer      *struct {
		ID   string `json:"id"`
		Site string `json:"site"`
	} `json:"trailer"`
}

// trailerURL builds a watch link from AniList's trailer field, which only
// ever names YouTube or Dailymotion.
func (m media) trailerURL() string {
	if m.Trailer == nil || m.Trailer.ID == "" {
		return ""
	}
	id := url.QueryEscape(m.Trailer.ID)
	switch strings.ToLower(m.Trailer.Site) {
	case "youtube":
		return "https://www.youtube.com/watch?v=" + id
	case "dailymotion":
		return "https://www.dailymotion.com/video/" + id
	}
	return ""
}

type page struct {
	PageInfo struct {
		LastPage    int  `json:"lastPage"`
		HasNextPage bool `json:"hasNextPage"`
	} `json:"pageInfo"`
	Media []media `json:"media"`
}

type recsData struct {
	Media *struct {
		Recommendations struct {
			Nodes []struct {
				Rating              int    `json:"rating"`
				MediaRecommendation *media `json:"mediaRecommendation"` // null if deleted
			} `json:"nodes"`
		} `json:"recommendations"`
	} `json:"Media"`
}

// Pick returns a random anime matching the mood and filters.
func (c *Client) Pick(ctx context.Context, m mood.Mood, f pick.Filters) (pick.Result, error) {
	vars := variables(m, f)
	res, err := c.pickFrom(ctx, vars, f, true)
	if errors.Is(err, pick.ErrNoResults) && vars["idNotIn"] != nil {
		// The cached page 1 ignores the seen list but other pages apply it,
		// so their numbering doesn't line up. When most matches are seen,
		// the unseen ones can fall between the two (they shift up to page 1
		// of the filtered results). Retry with the seen list applied to
		// every page, uncached, before saying there's nothing left.
		res, err = c.pickFrom(ctx, vars, f, false)
	}
	return res, err
}

// pickFrom picks a random title from the pages of a query. With cached set,
// page 1 comes from the cache when possible.
func (c *Client) pickFrom(ctx context.Context, vars map[string]any, f pick.Filters, cached bool) (pick.Result, error) {
	var (
		first cachedPage
		key   string
		err   error
	)
	if cached {
		first, key, err = c.firstPage(ctx, vars)
	} else {
		var pg page
		pg, err = c.fetch(ctx, 1, vars)
		first = newCachedPage(pg)
	}
	if err != nil {
		return pick.Result{}, err
	}

	fetch := func(ctx context.Context, p int) ([]media, error) {
		pg, err := c.fetch(ctx, p, vars)
		return pg.Media, err
	}
	// Other pages exclude seen titles server-side via id_not_in. The local
	// check covers the cached page 1 and a seen list longer than maxExcluded.
	keep := func(m media) bool { return !f.Exclude[itemID(m.ID)] }

	chosen, bound, err := pick.RandomFromPagesBound(ctx, first.Bound, first.Media, fetch, keep)
	if cached && bound < first.Bound {
		// Remember the real end of the results so the next pick doesn't
		// spend requests on the same empty pages.
		first.Bound = bound
		c.firstPages.Put(key, first)
	}
	if err != nil {
		return pick.Result{}, err
	}
	return toResult(chosen), nil
}

// cachedPage is page 1 of a query plus the highest page worth drawing from.
// AniList's lastPage on page 1 is an estimate that can be far too high
// (200 for a query with 2 pages), so Bound starts there, capped at maxPage,
// and shrinks as picks find empty pages.
type cachedPage struct {
	Media []media
	Bound int
}

func newCachedPage(pg page) cachedPage {
	bound := min(pg.PageInfo.LastPage, maxPage)
	if !pg.PageInfo.HasNextPage {
		bound = 1
	}
	return cachedPage{Media: pg.Media, Bound: max(bound, 1)}
}

// firstPage returns page 1 of a query from the cache, fetching it on a miss,
// along with its cache key. It's fetched and keyed without idNotIn, so one
// copy serves every seen list (each server's and DM's), and newly seen
// titles are still filtered out locally.
func (c *Client) firstPage(ctx context.Context, vars map[string]any) (cachedPage, string, error) {
	base := maps.Clone(vars)
	delete(base, "idNotIn")
	k, err := json.Marshal(base) // map keys are sorted, so this is stable
	if err != nil {
		return cachedPage{}, "", err
	}
	key := string(k)
	if cp, ok := c.firstPages.Get(key); ok {
		return cp, key, nil
	}
	pg, err := c.fetch(ctx, 1, base)
	if err != nil {
		return cachedPage{}, "", err
	}
	cp := newCachedPage(pg)
	c.firstPages.Put(key, cp)
	return cp, key, nil
}

// Similar returns a random anime from the top community recommendations for
// id (a Result ID like "anilist:21"), skipping excluded and low-rated ones.
func (c *Client) Similar(ctx context.Context, id string, f pick.Filters) (pick.Result, error) {
	n, ok := parseItemID(id)
	if !ok {
		return pick.Result{}, fmt.Errorf("anilist: not an AniList ID: %q", id)
	}
	var data recsData
	if err := c.post(ctx, recsQuery, map[string]any{"id": n, "perPage": perPage}, &data); err != nil {
		return pick.Result{}, err
	}
	if data.Media == nil {
		return pick.Result{}, pick.ErrNoResults
	}

	var candidates []media
	for _, node := range data.Media.Recommendations.Nodes {
		if m := node.MediaRecommendation; m != nil && node.Rating > 0 && keepSimilar(*m, f) {
			candidates = append(candidates, *m)
		}
	}
	if len(candidates) == 0 {
		return pick.Result{}, pick.ErrNoResults
	}
	return toResult(candidates[rand.IntN(len(candidates))]), nil
}

// keepSimilar applies the pick rules to a recommendation: same formats, no
// adult titles, rating at least f.MinRating, and not excluded. A rating of
// 0 means AniList has no score yet, which can't meet any minimum.
func keepSimilar(m media, f pick.Filters) bool {
	return pickFormats[m.Format] && !m.IsAdult &&
		float64(m.AverageScore) >= f.MinRating*10 &&
		!f.Exclude[itemID(m.ID)]
}

func itemID(id int) string { return "anilist:" + strconv.Itoa(id) }

func parseItemID(id string) (int, bool) {
	s, ok := strings.CutPrefix(id, "anilist:")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

// maxExcluded caps how many seen IDs go into id_not_in, to keep the
// request small. Anything beyond it is still filtered locally.
const maxExcluded = 500

// excludedIDs extracts AniList IDs from a set of result IDs.
func excludedIDs(exclude map[string]bool) []int {
	var ids []int
	for k := range exclude {
		if n, ok := strings.CutPrefix(k, "anilist:"); ok {
			if id, err := strconv.Atoi(n); err == nil {
				ids = append(ids, id)
			}
		}
		if len(ids) == maxExcluded {
			break
		}
	}
	return ids
}

// variables converts a mood and filters into AniList query variables.
// Filter ranges are inclusive; AniList's comparisons are exclusive.
func variables(m mood.Mood, f pick.Filters) map[string]any {
	vars := map[string]any{
		"perPage": perPage,
		// averageScore is 0–100; >= 7.5 becomes > 74.
		"minScore": int(math.Ceil(f.MinRating*10)) - 1,
	}
	if len(m.AnimeGenres) > 0 {
		vars["genres"] = m.AnimeGenres
	}
	if ids := excludedIDs(f.Exclude); len(ids) > 0 {
		vars["idNotIn"] = ids
	}
	if f.AnimeEpisodes.Min > 0 {
		vars["epGreater"] = f.AnimeEpisodes.Min - 1
	}
	if f.AnimeEpisodes.Max > 0 {
		vars["epLesser"] = f.AnimeEpisodes.Max + 1
	}
	// FuzzyDateInt is YYYYMMDD; year-only dates are stored as YYYY0000.
	if f.Years.Min > 0 {
		vars["startGreater"] = f.Years.Min*10000 - 1
	}
	if f.Years.Max > 0 {
		vars["startLesser"] = (f.Years.Max + 1) * 10000
	}
	return vars
}

func (c *Client) fetch(ctx context.Context, pageNum int, base map[string]any) (page, error) {
	vars := make(map[string]any, len(base)+1)
	for k, v := range base {
		vars[k] = v
	}
	vars["page"] = pageNum
	var data struct {
		Page page `json:"Page"`
	}
	err := c.post(ctx, query, vars, &data)
	return data.Page, err
}

// post runs a GraphQL query and decodes its "data" object into out.
func (c *Client) post(ctx context.Context, q string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("anilist request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return pick.RateLimited("AniList", resp.Header)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("anilist decode (status %d): %w", resp.StatusCode, err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("anilist: %s", envelope.Errors[0].Message)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anilist: unexpected status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("anilist decode data: %w", err)
	}
	return nil
}

func toResult(m media) pick.Result {
	title := m.Title.English
	if title == "" {
		title = m.Title.Romaji
	}

	var fields []pick.Field
	if m.AverageScore > 0 {
		fields = append(fields, pick.Field{Name: "Score", Value: fmt.Sprintf("%.1f/10", float64(m.AverageScore)/10)})
	}
	format := formatLabel(m.Format)
	if m.Episodes > 1 {
		format += fmt.Sprintf(" · %d eps", m.Episodes)
	}
	fields = append(fields, pick.Field{Name: "Format", Value: format})
	if m.SeasonYear > 0 {
		fields = append(fields, pick.Field{Name: "Year", Value: strconv.Itoa(m.SeasonYear)})
	}
	if len(m.Genres) > 0 {
		fields = append(fields, pick.Field{Name: "Genres", Value: strings.Join(m.Genres, ", ")})
	}

	color := 0
	if hex := strings.TrimPrefix(m.CoverImage.Color, "#"); hex != "" {
		if v, err := strconv.ParseInt(hex, 16, 32); err == nil {
			color = int(v)
		}
	}

	return pick.Result{
		ID:          itemID(m.ID),
		Kind:        pick.Anime,
		Title:       title,
		URL:         m.SiteURL,
		Description: pick.CleanText(m.Description, 400),
		ImageURL:    m.CoverImage.ExtraLarge,
		Color:       color,
		Fields:      fields,
		Source:      "AniList",
		TrailerURL:  m.trailerURL(),
	}
}

func formatLabel(f string) string {
	switch f {
	case "TV":
		return "TV series"
	case "MOVIE":
		return "Movie"
	case "ONA":
		return "Web series"
	}
	return f
}
