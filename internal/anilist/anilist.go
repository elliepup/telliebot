// Package anilist picks random anime from the AniList GraphQL API.
// No API key is required.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
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

// Null variables are ignored by AniList, so unset filters simply drop out.
// The *_greater / *_lesser arguments are exclusive.
const query = `query ($page: Int, $perPage: Int, $genres: [String], $minScore: Int,
                      $epGreater: Int, $epLesser: Int,
                      $startGreater: FuzzyDateInt, $startLesser: FuzzyDateInt,
                      $idNotIn: [Int]) {
  Page(page: $page, perPage: $perPage) {
    pageInfo { lastPage }
    media(type: ANIME, isAdult: false, genre_in: $genres, format_in: [TV, MOVIE, ONA],
          id_not_in: $idNotIn,
          averageScore_greater: $minScore,
          episodes_greater: $epGreater, episodes_lesser: $epLesser,
          startDate_greater: $startGreater, startDate_lesser: $startLesser,
          sort: POPULARITY_DESC) {
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
      trailer { id site }
    }
  }
}`

// Client talks to AniList.
type Client struct {
	http *http.Client
}

// New returns an AniList client.
func New() *Client {
	return &Client{http: &http.Client{Timeout: 10 * time.Second}}
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
		LastPage int `json:"lastPage"`
	} `json:"pageInfo"`
	Media []media `json:"media"`
}

type response struct {
	Data struct {
		Page page `json:"Page"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Pick returns a random anime matching the mood and filters.
func (c *Client) Pick(ctx context.Context, m mood.Mood, f pick.Filters) (pick.Result, error) {
	vars := variables(m, f)

	first, err := c.fetch(ctx, 1, vars)
	if err != nil {
		return pick.Result{}, err
	}

	fetch := func(ctx context.Context, p int) ([]media, error) {
		pg, err := c.fetch(ctx, p, vars)
		return pg.Media, err
	}
	// Seen titles are already excluded server-side via id_not_in; checking
	// again locally guards against the list being truncated.
	keep := func(m media) bool { return !f.Exclude[itemID(m.ID)] }

	chosen, err := pick.RandomFromPages(ctx, min(first.PageInfo.LastPage, maxPage), first.Media, fetch, keep)
	if err != nil {
		return pick.Result{}, err
	}
	return toResult(chosen), nil
}

func itemID(id int) string { return "anilist:" + strconv.Itoa(id) }

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
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return page{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return page{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return page{}, fmt.Errorf("anilist request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return page{}, fmt.Errorf("anilist rate limited (retry after %ss)", resp.Header.Get("Retry-After"))
	}

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return page{}, fmt.Errorf("anilist decode (status %d): %w", resp.StatusCode, err)
	}
	if len(out.Errors) > 0 {
		return page{}, fmt.Errorf("anilist: %s", out.Errors[0].Message)
	}
	if resp.StatusCode != http.StatusOK {
		return page{}, fmt.Errorf("anilist: unexpected status %d", resp.StatusCode)
	}
	return out.Data.Page, nil
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
