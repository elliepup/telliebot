package anilist

import "testing"

func TestTrailerURL(t *testing.T) {
	type tr = struct {
		ID   string `json:"id"`
		Site string `json:"site"`
	}
	cases := []struct {
		trailer *tr
		want    string
	}{
		{nil, ""},
		{&tr{ID: "", Site: "youtube"}, ""},
		{&tr{ID: "abc123", Site: "youtube"}, "https://www.youtube.com/watch?v=abc123"},
		{&tr{ID: "x7zz", Site: "dailymotion"}, "https://www.dailymotion.com/video/x7zz"},
		{&tr{ID: "abc", Site: "unknown"}, ""},
		{&tr{ID: "a&b=c", Site: "youtube"}, "https://www.youtube.com/watch?v=a%26b%3Dc"},
	}
	for _, c := range cases {
		m := media{Trailer: c.trailer}
		if got := m.trailerURL(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.trailer, got, c.want)
		}
	}
}
