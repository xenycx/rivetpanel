package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/xenycx/rivetpanel/internal/config"
)

func TestListenCheckWarnsOnlyForExposedPlainHTTP(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		listen, url string
		prod, warn  bool
	}{
		{"0.0.0.0:8080", "http://panel.example.com", true, true},
		{":8080", "http://203.0.113.4:8080", true, true},
		{"127.0.0.1:8080", "http://panel.example.com", true, false},
		{"[::1]:8080", "http://panel.example.com", true, false},
		{"0.0.0.0:8080", "https://panel.example.com", true, false},
		{"0.0.0.0:8080", "http://localhost:8080", true, false},
		{"0.0.0.0:8080", "http://panel.example.com", false, false},
		{"0.0.0.0:8080", "", true, false},
	}
	for _, c := range cases {
		got := listenCheck(quiet, config.Config{Listen: c.listen, PublicURL: c.url, Production: c.prod})
		if (len(got) > 0) != c.warn {
			t.Errorf("listen %s url %q prod %v: warned=%v, want %v", c.listen, c.url, c.prod, len(got) > 0, c.warn)
		}
	}
}
