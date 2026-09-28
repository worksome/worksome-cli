package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const statusFixture = `{
  "summarizedStatus": "warning",
  "pinnedUpdate": {"title": "Scheduled maintenance", "severity": "scheduled"},
  "monitors": {
    "production": [{"label": "Gateway", "url": "https://api.example/health", "status": "down"}],
    "commercial": [{"label": "Website", "url": "https://example", "status": "up"}]
  },
  "updatesPerDay": {
    "1790467200": [{"title": "Older", "severity": "resolved"}],
    "1790553600": [{"title": "Newer", "severity": "high"}],
    "1790380800": []
  }
}`

func TestFetchStatusSummarizesFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(statusFixture))
	}))
	defer srv.Close()

	s, err := fetchStatus(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	if s.Status != "warning" || s.Pinned == nil || s.Pinned.Title != "Scheduled maintenance" {
		t.Fatalf("status/pinned = %q/%+v", s.Status, s.Pinned)
	}
	if len(s.Monitors) != 2 || s.Monitors[0].Group != "commercial" || s.Monitors[1].Label != "Gateway" {
		t.Fatalf("monitors not sorted by group: %+v", s.Monitors)
	}
	if len(s.Updates) != 2 || s.Updates[0].Title != "Newer" || s.Updates[0].Date != "2026-09-28" || s.Updates[1].Date != "2026-09-27" {
		t.Fatalf("updates not newest-first with dates: %+v", s.Updates)
	}

	out := formatStatus(s)
	for _, want := range []string{"Overall: warning", "[scheduled] Scheduled maintenance", "  down   Gateway", "2026-09-28  [high] Newer"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFetchStatusRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := fetchStatus(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v, want HTTP 502", err)
	}
}
