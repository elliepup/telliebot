package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

// fakeTMDB serves discover (3 pages of 20) and empty video lists, and
// counts discover requests by page.
type fakeTMDB struct {
	status int

	mu            sync.Mutex
	page1, others int
	sawKey        bool
}

func (f *fakeTMDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.status != 0 {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(f.status)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/videos") {
		fmt.Fprint(w, `{"results":[]}`)
		return
	}
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	f.mu.Lock()
	if p == 1 {
		f.page1++
	} else {
		f.others++
	}
	f.sawKey = f.sawKey || r.URL.Query().Get("api_key") == "k"
	f.mu.Unlock()

	results := make([]string, 0, 20)
	for k := 1; k <= 20; k++ {
		id := p*100 + k
		results = append(results, fmt.Sprintf(`{"id":%d,"title":"M%d","vote_average":7.5,"vote_count":500,"original_language":"en"}`, id, id))
	}
	fmt.Fprintf(w, `{"total_pages":3,"results":[%s]}`, strings.Join(results, ","))
}

func newFakeTMDB(t *testing.T, f *fakeTMDB) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New("k")
	c.base = srv.URL
	return c
}

func TestPickCachesFirstPage(t *testing.T) {
	fake := &fakeTMDB{}
	c := newFakeTMDB(t, fake)
	ctx := context.Background()
	m, _ := mood.Get("funny")
	f := pick.ParseFilters("7", "short", "")

	for k := 0; k < 5; k++ {
		f.Exclude = map[string]bool{itemID(pick.Movie, 101+k): true} // seen lists vary
		if _, err := c.Movies().Pick(ctx, m, f); err != nil {
			t.Fatal(err)
		}
	}
	if page1, _ := fake.counts(); page1 != 1 {
		t.Errorf("5 identical picks fetched page 1 %d times, want 1", page1)
	}
	if _, sawKey := fake.counts(); !sawKey {
		t.Error("requests should still carry the API key")
	}

	if _, err := c.Shows().Pick(ctx, m, f); err != nil {
		t.Fatal(err)
	}
	if page1, _ := fake.counts(); page1 != 2 {
		t.Errorf("movies and shows must not share a cached page: page 1 fetched %d times", page1)
	}
}

func TestRateLimitedPick(t *testing.T) {
	c := newFakeTMDB(t, &fakeTMDB{status: http.StatusTooManyRequests})
	_, err := c.Movies().Pick(context.Background(), mood.Surprise, pick.ParseFilters("", "", ""))
	var rl *pick.RateLimitError
	if !errors.As(err, &rl) || rl.Source != "TMDB" || rl.RetryAfter != 3*time.Second {
		t.Fatalf("got %v, want a TMDB RateLimitError with 3s", err)
	}
}

func (f *fakeTMDB) counts() (page1 int, sawKey bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.page1, f.sawKey
}
