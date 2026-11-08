// Package anilist picks random anime from the AniList GraphQL API.
// No API key is required.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
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

const query = `query ($page: Int, $perPage: Int, $genres: [String]) {
  Page(page: $page, perPage: $perPage) {
    pageInfo { lastPage }
    media(type: ANIME, isAdult: false, genre_in: $genres, format_in: [TV, MOVIE, ONA],
          averageScore_greater: 65, sort: POPULARITY_DESC) {
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

// Pick returns a random anime matching the mood.
func (c *Client) Pick(ctx context.Context, m mood.Mood) (pick.Result, error) {
	first, err := c.fetch(ctx, 1, m.AnimeGenres)
	if err != nil {
		return pick.Result{}, err
	}

	items := first.Media
	if last := min(first.PageInfo.LastPage, maxPage); last > 1 {
		if p := rand.IntN(last) + 1; p != 1 {
			pg, err := c.fetch(ctx, p, m.AnimeGenres)
			if err != nil {
				return pick.Result{}, err
			}
			if len(pg.Media) > 0 {
				items = pg.Media
			}
		}
	}
	if len(items) == 0 {
		return pick.Result{}, pick.ErrNoResults
	}
	return toResult(items[rand.IntN(len(items))]), nil
}

func (c *Client) fetch(ctx context.Context, pageNum int, genres []string) (page, error) {
	vars := map[string]any{"page": pageNum, "perPage": perPage}
	if len(genres) > 0 {
		vars["genres"] = genres
	}
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
		Kind:        pick.Anime,
		Title:       title,
		URL:         m.SiteURL,
		Description: pick.CleanText(m.Description, 400),
		ImageURL:    m.CoverImage.ExtraLarge,
		Color:       color,
		Fields:      fields,
		Source:      "AniList",
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
