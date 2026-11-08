// Package tmdb picks random movies and shows from The Movie Database.
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
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
	baseURL   = "https://api.themoviedb.org/3"
	imageBase = "https://image.tmdb.org/t/p/w500"
	siteBase  = "https://www.themoviedb.org"
	// maxPage limits picks to the top maxPage*20 most popular matches.
	maxPage = 25
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

func (s source) Pick(ctx context.Context, m mood.Mood) (pick.Result, error) {
	return s.c.pick(ctx, s.kind, m)
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
}

type discoverResponse struct {
	TotalPages int      `json:"total_pages"`
	Results    []result `json:"results"`
}

func (c *Client) pick(ctx context.Context, kind pick.Kind, m mood.Mood) (pick.Result, error) {
	path, genres := "/discover/movie", m.MovieGenres
	if kind == pick.Show {
		path, genres = "/discover/tv", m.ShowGenres
	}

	first, err := c.discover(ctx, path, genres, 1)
	if err != nil {
		return pick.Result{}, err
	}
	items := first.Results
	if last := min(first.TotalPages, maxPage); last > 1 {
		if p := rand.IntN(last) + 1; p != 1 {
			pg, err := c.discover(ctx, path, genres, p)
			if err != nil {
				return pick.Result{}, err
			}
			if len(pg.Results) > 0 {
				items = pg.Results
			}
		}
	}
	if len(items) == 0 {
		return pick.Result{}, pick.ErrNoResults
	}
	return toResult(kind, items[rand.IntN(len(items))]), nil
}

func (c *Client) discover(ctx context.Context, path string, genres []int, page int) (discoverResponse, error) {
	q := url.Values{}
	q.Set("include_adult", "false")
	q.Set("sort_by", "popularity.desc")
	q.Set("vote_count.gte", "200")
	q.Set("vote_average.gte", "6.5")
	q.Set("page", strconv.Itoa(page))
	if len(genres) > 0 {
		ids := make([]string, len(genres))
		for i, g := range genres {
			ids[i] = strconv.Itoa(g)
		}
		q.Set("with_genres", strings.Join(ids, "|")) // | = OR
	}

	bearer := strings.HasPrefix(c.key, "eyJ")
	if !bearer {
		q.Set("api_key", c.key)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return discoverResponse{}, err
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
		return discoverResponse{}, fmt.Errorf("tmdb request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return discoverResponse{}, fmt.Errorf("tmdb: unexpected status %d", resp.StatusCode)
	}
	var out discoverResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return discoverResponse{}, fmt.Errorf("tmdb decode: %w", err)
	}
	return out, nil
}

func toResult(kind pick.Kind, r result) pick.Result {
	title, date, segment := r.Title, r.ReleaseDate, "movie"
	if kind == pick.Show {
		title, date, segment = r.Name, r.FirstAirDate, "tv"
	}

	var fields []pick.Field
	if r.VoteAverage > 0 {
		fields = append(fields, pick.Field{Name: "Rating", Value: fmt.Sprintf("%.1f/10", r.VoteAverage)})
	}
	if len(date) >= 4 {
		fields = append(fields, pick.Field{Name: "Year", Value: date[:4]})
	}

	res := pick.Result{
		Kind:        kind,
		Title:       title,
		URL:         fmt.Sprintf("%s/%s/%d", siteBase, segment, r.ID),
		Description: pick.CleanText(r.Overview, 400),
		Fields:      fields,
		Source:      "TMDB",
	}
	if r.PosterPath != "" {
		res.ImageURL = imageBase + r.PosterPath
	}
	return res
}
