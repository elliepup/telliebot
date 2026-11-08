package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elliepup/telliebot/internal/mood"
	"github.com/elliepup/telliebot/internal/pick"
)

// fakeAniList serves the Page query over titles 1..total, applying idNotIn
// and paging like AniList does, and counts requests.
type fakeAniList struct {
	total      int
	status     int // non-zero: answer every request with this status
	retryAfter string
	// claimLast, if set, is the lastPage reported on page 1, like real
	// AniList, which estimates it (200 for a 2-page query).
	claimLast int

	mu            sync.Mutex
	page1, others int
	page1Excluded int // page-1 requests that carried idNotIn
	empty         int // requests for pages past the end
}

func (f *fakeAniList) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Variables struct {
			Page    int   `json:"page"`
			PerPage int   `json:"perPage"`
			IDNotIn []int `json:"idNotIn"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if f.status != 0 {
		w.Header().Set("Retry-After", f.retryAfter)
		w.WriteHeader(f.status)
		fmt.Fprint(w, `{"data":null,"errors":[{"message":"Too Many Requests."}]}`)
		return
	}
	v := body.Variables

	f.mu.Lock()
	if v.Page == 1 {
		f.page1++
		if len(v.IDNotIn) > 0 {
			f.page1Excluded++
		}
	} else {
		f.others++
	}
	f.mu.Unlock()

	excluded := map[int]bool{}
	for _, id := range v.IDNotIn {
		excluded[id] = true
	}
	var ids []int
	for id := 1; id <= f.total; id++ {
		if !excluded[id] {
			ids = append(ids, id)
		}
	}
	lastPage := max(1, (len(ids)+v.PerPage-1)/v.PerPage)
	start := min((v.Page-1)*v.PerPage, len(ids))
	end := min(start+v.PerPage, len(ids))
	hasNext := end < len(ids)
	reported := lastPage
	if v.Page == 1 && f.claimLast > 0 {
		reported = f.claimLast
	}
	if start == end {
		reported = v.Page // AniList echoes the requested page when it's empty
		f.mu.Lock()
		f.empty++
		f.mu.Unlock()
	}

	media := make([]string, 0, end-start)
	for _, id := range ids[start:end] {
		media = append(media, fmt.Sprintf(`{"id":%d,"title":{"romaji":"T%d"},"format":"TV","averageScore":80}`, id, id))
	}
	fmt.Fprintf(w, `{"data":{"Page":{"pageInfo":{"lastPage":%d,"hasNextPage":%t},"media":[%s]}}}`,
		reported, hasNext, strings.Join(media, ","))
}

func (f *fakeAniList) emptyRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.empty
}

func TestLearnsRealLastPage(t *testing.T) {
	// 35 titles = 2 real pages, but page 1 claims 200, like the real API.
	fake := &fakeAniList{total: 35, claimLast: 200}
	c := newFake(t, fake)
	ctx := context.Background()
	seenIDs := map[string]bool{}
	for k := 0; k < 40; k++ {
		r, err := c.Pick(ctx, mood.Surprise, pick.Filters{MinRating: 7})
		if err != nil {
			t.Fatal(err)
		}
		seenIDs[r.ID] = true
	}
	early := fake.emptyRequests()
	for k := 0; k < 20; k++ {
		if _, err := c.Pick(ctx, mood.Surprise, pick.Filters{MinRating: 7}); err != nil {
			t.Fatal(err)
		}
	}
	if late := fake.emptyRequests() - early; late != 0 {
		t.Errorf("after learning the real end, picks still requested %d empty pages", late)
	}
	page2 := false
	for id := range seenIDs {
		if n, _ := parseItemID(id); n > perPage {
			page2 = true
		}
	}
	if !page2 {
		t.Error("titles on page 2 were never picked")
	}
	t.Logf("empty-page requests while learning: %d (over 40 picks)", early)
}

func TestSinglePageNeedsOneRequest(t *testing.T) {
	// hasNextPage=false on page 1 means there's nothing else to fetch.
	fake := &fakeAniList{total: 10, claimLast: 200}
	c := newFake(t, fake)
	for k := 0; k < 5; k++ {
		if _, err := c.Pick(context.Background(), mood.Surprise, pick.Filters{MinRating: 7}); err != nil {
			t.Fatal(err)
		}
	}
	if page1, others, _ := fake.counts(); page1 != 1 || others != 0 {
		t.Errorf("5 picks of a 1-page query: page1=%d others=%d, want 1 and 0", page1, others)
	}
}

func (f *fakeAniList) counts() (page1, others, page1Excluded int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.page1, f.others, f.page1Excluded
}

func newFake(t *testing.T, f *fakeAniList) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New()
	c.endpoint = srv.URL
	return c
}

func TestPickCachesFirstPage(t *testing.T) {
	fake := &fakeAniList{total: 100} // 4 pages
	c := newFake(t, fake)
	ctx := context.Background()
	m, _ := mood.Get("cozy")
	f := pick.ParseFilters("7", "", "")

	for k := 0; k < 5; k++ {
		if _, err := c.Pick(ctx, m, f); err != nil {
			t.Fatal(err)
		}
	}
	page1, others, _ := fake.counts()
	if page1 != 1 {
		t.Errorf("5 identical picks fetched page 1 %d times, want 1", page1)
	}
	if others > 5 {
		t.Errorf("each pick should need at most one more request, got %d for 5 picks", others)
	}

	if _, err := c.Pick(ctx, m, pick.ParseFilters("8", "", "")); err != nil {
		t.Fatal(err)
	}
	if page1, _, _ := fake.counts(); page1 != 2 {
		t.Errorf("different filters must not share a cached page: page 1 fetched %d times, want 2", page1)
	}
}

func TestCachedFirstPageIgnoresSeenList(t *testing.T) {
	fake := &fakeAniList{total: 100}
	c := newFake(t, fake)
	ctx := context.Background()

	seen := map[string]bool{}
	for id := 1; id <= 20; id++ {
		seen[itemID(id)] = true
	}
	for k := 0; k < 40; k++ {
		// Different seen lists (like different servers) share page 1.
		f := pick.Filters{MinRating: 7, Exclude: seen}
		if k%2 == 1 {
			f.Exclude = map[string]bool{itemID(99): true}
		}
		r, err := c.Pick(ctx, mood.Surprise, f)
		if err != nil {
			t.Fatal(err)
		}
		if f.Exclude[r.ID] {
			t.Fatalf("picked seen title %s", r.ID)
		}
	}
	page1, _, page1Excluded := fake.counts()
	if page1 != 1 || page1Excluded != 0 {
		t.Errorf("page 1: fetched %d times (%d with idNotIn), want once without it", page1, page1Excluded)
	}
}

func TestHeavilySeenQueryFallsBackToFilteredPages(t *testing.T) {
	// 30 matches, 25 per page. With 1..25 seen, unfiltered page 1 is all
	// seen and filtered page 2 is empty: the 5 unseen titles are only on
	// filtered page 1. The cached path alone would wrongly say "no results".
	fake := &fakeAniList{total: 30}
	c := newFake(t, fake)
	seen := map[string]bool{}
	for id := 1; id <= 25; id++ {
		seen[itemID(id)] = true
	}
	for k := 0; k < 10; k++ {
		r, err := c.Pick(context.Background(), mood.Surprise, pick.Filters{MinRating: 7, Exclude: seen})
		if err != nil {
			t.Fatalf("pick %d: %v (5 unseen titles remain)", k, err)
		}
		if seen[r.ID] {
			t.Fatalf("picked seen title %s", r.ID)
		}
	}
}

func TestEverythingSeenStillReportsNoResults(t *testing.T) {
	fake := &fakeAniList{total: 10}
	c := newFake(t, fake)
	seen := map[string]bool{}
	for id := 1; id <= 10; id++ {
		seen[itemID(id)] = true
	}
	_, err := c.Pick(context.Background(), mood.Surprise, pick.Filters{MinRating: 7, Exclude: seen})
	if !errors.Is(err, pick.ErrNoResults) {
		t.Fatalf("got %v, want ErrNoResults", err)
	}
}

func TestRateLimitedPick(t *testing.T) {
	c := newFake(t, &fakeAniList{total: 10, status: http.StatusTooManyRequests, retryAfter: "57"})
	_, err := c.Pick(context.Background(), mood.Surprise, pick.Filters{MinRating: 7})
	var rl *pick.RateLimitError
	if !errors.As(err, &rl) || rl.Source != "AniList" || rl.RetryAfter != 57*time.Second {
		t.Fatalf("got %v, want an AniList RateLimitError with 57s", err)
	}
}
