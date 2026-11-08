package pick

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCacheTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewCache[int](10, time.Minute)
	c.now = func() time.Time { return now }

	c.Put("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("fresh entry: got %d %v", v, ok)
	}
	now = now.Add(59 * time.Second)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("entry should still be fresh at 59s")
	}
	now = now.Add(time.Second)
	if _, ok := c.Get("a"); ok {
		t.Fatal("entry should expire at the TTL")
	}
	if c.Len() != 0 {
		t.Fatalf("expired entry should be dropped, len %d", c.Len())
	}
}

func TestCacheLRUEviction(t *testing.T) {
	c := NewCache[string](2, time.Hour)
	c.Put("a", "A")
	c.Put("b", "B")
	c.Get("a")      // a is now most recently used
	c.Put("c", "C") // evicts b
	if _, ok := c.Get("b"); ok {
		t.Error("least recently used entry should be evicted")
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := c.Get(k); !ok {
			t.Errorf("%s should still be cached", k)
		}
	}
	c.Put("a", "A2") // overwrite keeps size
	if v, _ := c.Get("a"); v != "A2" || c.Len() != 2 {
		t.Errorf("overwrite: got %q, len %d", v, c.Len())
	}
}

func TestCacheConcurrent(t *testing.T) {
	c := NewCache[int](8, time.Hour)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				key := string(rune('a' + (g+k)%12))
				c.Put(key, k)
				c.Get(key)
			}
		}(g)
	}
	wg.Wait()
	if c.Len() > 8 {
		t.Fatalf("cache grew past its limit: %d", c.Len())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"57":                            57 * time.Second,
		" 0 ":                           0,
		"-5":                            0,
		"":                              0,
		"soon":                          0,
		"Sun, 08 Nov 2026 12:01:30 GMT": 90 * time.Second,
		"Sun, 08 Nov 2026 11:00:00 GMT": 0, // in the past
	}
	for in, want := range cases {
		if got := ParseRetryAfter(in, now); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

func TestRateLimited(t *testing.T) {
	e := RateLimited("AniList", http.Header{"Retry-After": {"57"}})
	if e.RetryAfter != 57*time.Second || !strings.Contains(e.Error(), "AniList rate limited") {
		t.Fatalf("got %+v / %q", e, e.Error())
	}
}
