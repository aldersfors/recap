package activity

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIsBot(t *testing.T) {
	for login, want := range map[string]bool{
		"renovate[bot]":         true,
		"project_42_bot_abc123": true,
		"group_7_bot_ff":        true,
		"project_manager":       false,
		"alice":                 false,
		"":                      false,
	} {
		if got := isBot(login); got != want {
			t.Errorf("isBot(%q) = %v, want %v", login, got, want)
		}
	}
}

func TestFilterAndSort(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	items := []Item{
		{Repo: "b", Author: "Alice", At: day(22)},
		{Repo: "a", Author: "bob", At: day(24)},
		{Repo: "a", Author: "carol", At: day(23)},
		{Repo: "a", Author: "project_1_bot_x", At: day(21)},
	}
	// filterAndSort edits its input in place, so each call gets a fresh copy.
	got := filterAndSort(slices.Clone(items), map[string]bool{"alice": true, "bob": true, "project_1_bot_x": true}, []string{"bob"}, true)
	if len(got) != 1 || got[0].Author != "Alice" {
		t.Fatalf("got %+v, want only Alice (include is case-insensitive, exclude and bots still apply)", got)
	}
	got = filterAndSort(slices.Clone(items[:3]), nil, nil, false)
	var order []string
	for _, it := range got {
		order = append(order, it.Repo+"/"+it.Author)
	}
	if want := "a/carol a/bob b/Alice"; strings.Join(order, " ") != want {
		t.Errorf("order = %q, want %q", strings.Join(order, " "), want)
	}
}

func TestGitHubCollectorSetsWindow(t *testing.T) {
	srv := fakeGitHub(t)
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	var c Collector = GitHubCollector{
		Client: &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()},
		Query:  Query{Org: "acme", Repos: []string{"platform"}, Topics: []string{"ops"}},
	}
	items, err := c.Collect(context.Background(), from, from.AddDate(0, 0, 7))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Errorf("got %d items, want 3", len(items))
	}
}

func TestGetPagesRefusesNextOnAnotherHost(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("token-bearing request followed to another host: %s %q", r.URL, r.Header.Get("PRIVATE-TOKEN"))
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", fmt.Sprintf(`<%s/page2>; rel="next"`, other.URL))
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer first.Close()
	h := http.Header{}
	h.Set("PRIVATE-TOKEN", "tok")
	err := getPages(context.Background(), first.Client(), first.URL+"/page1", h, func([]byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("err = %v, want a refusal to follow Link to another host", err)
	}
}

func TestStoredItemsDropEmailAuthors(t *testing.T) {
	in := []Item{{Kind: KindMR, Author: "alice"}, {Kind: KindCommit, Author: "Bob@Corp.example"}}
	got := ForStorage(in)
	if got[0].Author != "alice" || got[1].Author != "" {
		t.Errorf("stored authors = %q, %q; want the login kept and the email dropped", got[0].Author, got[1].Author)
	}
	if in[1].Author != "Bob@Corp.example" {
		t.Error("ForStorage must not modify its input")
	}
}
