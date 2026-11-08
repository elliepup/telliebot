package pick

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitError means a source answered 429 Too Many Requests.
type RateLimitError struct {
	Source     string        // e.g. "AniList"
	RetryAfter time.Duration // 0 if the source didn't say
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s rate limited (retry after %s)", e.Source, e.RetryAfter)
	}
	return e.Source + " rate limited"
}

// RateLimited builds a RateLimitError from a 429 response's headers.
func RateLimited(source string, h http.Header) *RateLimitError {
	return &RateLimitError{Source: source, RetryAfter: ParseRetryAfter(h.Get("Retry-After"), time.Now())}
}

// ParseRetryAfter reads a Retry-After header, which is either a number of
// seconds or an HTTP date. It returns 0 when the header is missing or bad.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil {
		return time.Duration(max(s, 0)) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now).Round(time.Second)
	}
	return 0
}
