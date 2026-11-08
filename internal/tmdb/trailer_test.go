package tmdb

import "testing"

func TestBestVideo(t *testing.T) {
	cases := []struct {
		name string
		in   []video
		want string // key; "" means none
	}{
		{"empty", nil, ""},
		{"only featurettes", []video{{Key: "f", Site: "YouTube", Type: "Featurette"}}, ""},
		{"non-youtube ignored", []video{{Key: "v", Site: "Vimeo", Type: "Trailer", Official: true}}, ""},
		{"trailer beats teaser", []video{
			{Key: "teaser", Site: "YouTube", Type: "Teaser", Official: true, Language: "en"},
			{Key: "trailer", Site: "YouTube", Type: "Trailer"},
		}, "trailer"},
		{"official beats unofficial", []video{
			{Key: "fan", Site: "YouTube", Type: "Trailer", Language: "en"},
			{Key: "official", Site: "YouTube", Type: "Trailer", Official: true, Language: "ko"},
		}, "official"},
		{"english breaks ties", []video{
			{Key: "ko", Site: "YouTube", Type: "Trailer", Official: true, Language: "ko"},
			{Key: "en", Site: "YouTube", Type: "Trailer", Official: true, Language: "en"},
		}, "en"},
		{"teaser when no trailer", []video{
			{Key: "clip", Site: "YouTube", Type: "Clip"},
			{Key: "teaser", Site: "YouTube", Type: "Teaser"},
		}, "teaser"},
	}
	for _, c := range cases {
		v, ok := bestVideo(c.in)
		if got := map[bool]string{true: v.Key, false: ""}[ok]; got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
