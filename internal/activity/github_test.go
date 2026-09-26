package activity

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/search/repositories", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("q"); q != "org:acme topic:ops archived:false" {
			t.Errorf("repo search q = %q", q)
		}
		_, _ = fmt.Fprint(w, `{"items":[{"name":"platform"},{"name":"tools"}]}`)
	})
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		q := r.URL.Query().Get("q")
		if r.URL.Query().Get("page") == "" && !strings.Contains(q, "is:pr is:merged merged:2026-09-21..2026-09-27") {
			t.Errorf("pr search q = %q", q)
		}
		switch {
		case strings.Contains(q, "repo:acme/platform") && r.URL.Query().Get("page") == "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/search/issues?q=x&page=2>; rel="next", <%s/search/issues?q=x&page=2>; rel="last"`, srv.URL, srv.URL))
			_, _ = fmt.Fprint(w, `{"items":[{"number":12,"title":"Add Grafana SSO","body":"Uses Keycloak","html_url":"https://gh/acme/platform/pull/12","user":{"login":"alice"},"labels":[{"name":"feature"}],"pull_request":{"merged_at":"2026-09-22T10:00:00Z"}}]}`)
		case r.URL.Query().Get("page") == "2":
			_, _ = fmt.Fprint(w, `{"items":[{"number":13,"title":"Bump chart","html_url":"https://gh/acme/platform/pull/13","user":{"login":"renovate[bot]"},"pull_request":{"merged_at":"2026-09-23T10:00:00Z"}}]}`)
		default:
			_, _ = fmt.Fprint(w, `{"items":[]}`)
		}
	})
	mux.HandleFunc("/repos/acme/platform/commits", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") != "2026-09-21T00:00:00Z" || r.URL.Query().Get("until") != "2026-09-28T00:00:00Z" {
			t.Errorf("commits query = %s", r.URL.RawQuery)
		}
		_, _ = fmt.Fprint(w, `[
		  {"sha":"a1","html_url":"https://gh/c/a1","author":{"login":"alice"},"parents":[{}],"commit":{"message":"Add Grafana SSO (#12)","author":{"date":"2026-09-22T10:00:00Z"}}},
		  {"sha":"b2","html_url":"https://gh/c/b2","author":null,"parents":[{}],"commit":{"message":"Rotate backup key\n\nYearly rotation.","author":{"date":"2026-09-24T09:00:00Z"}}},
		  {"sha":"c3","html_url":"https://gh/c/c3","author":{"login":"bob"},"parents":[{},{}],"commit":{"message":"Merge branch main","author":{"date":"2026-09-24T09:30:00Z"}}}
		]`)
	})
	mux.HandleFunc("/orgs/acme/teams/ops/members", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `[{"login":"Alice"}]`)
	})
	mux.HandleFunc("/repos/acme/tools/commits", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `[]`)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCollect(t *testing.T) {
	srv := fakeGitHub(t)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	items, err := c.Collect(context.Background(), Query{
		Org:            "acme",
		Repos:          []string{"platform"},
		Topics:         []string{"ops"},
		ExcludeAuthors: []string{"renovate[bot]"},
		From:           from,
		To:             from.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var got []string
	for _, it := range items {
		got = append(got, fmt.Sprintf("%s %s %s %s", it.Repo, it.Kind, it.Title, it.Author))
	}
	want := []string{
		"platform pr Add Grafana SSO alice",
		"platform commit Rotate backup key ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("items:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if items[0].Labels[0] != "feature" || items[0].Body != "Uses Keycloak" {
		t.Errorf("pr item = %+v", items[0])
	}
	if items[1].Body != "Yearly rotation." {
		t.Errorf("commit body = %q", items[1].Body)
	}
}

func TestCollectReportsHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Collect(context.Background(), Query{Org: "acme", Repos: []string{"x"}, From: time.Now(), To: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want 401", err)
	}
}

func TestMergedRangeIncludesPartialLastDay(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/issues" {
			gotQ = r.URL.Query().Get("q")
			_, _ = fmt.Fprint(w, `{"items":[]}`)
			return
		}
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 25, 14, 30, 0, 0, time.UTC) // window clamped to "now"
	if _, err := c.Collect(context.Background(), Query{Org: "acme", Repos: []string{"x"}, From: from, To: to}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQ, "merged:2026-09-21..2026-09-25") {
		t.Errorf("q = %q, want the range to include 2026-09-25", gotQ)
	}
}

func TestExcludeBots(t *testing.T) {
	srv := fakeGitHub(t)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	items, err := c.Collect(context.Background(), Query{
		Org: "acme", Repos: []string{"platform"}, Topics: []string{"ops"},
		ExcludeBots: true, From: from, To: from.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if strings.HasSuffix(it.Author, "[bot]") {
			t.Errorf("bot item kept: %+v", it)
		}
	}
	if len(items) != 2 {
		t.Errorf("got %d items, want 2", len(items))
	}
}

func TestIncludeTeamsAndAuthors(t *testing.T) {
	srv := fakeGitHub(t)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		teams   []string
		authors []string
		want    []string
	}{
		{"no filter keeps everyone", nil, nil, []string{"alice", "renovate[bot]", ""}},
		{"team only", []string{"ops"}, nil, []string{"alice"}},
		{"team plus author", []string{"ops"}, []string{"Renovate[bot]"}, []string{"alice", "renovate[bot]"}},
		{"author only", nil, []string{"renovate[bot]"}, []string{"renovate[bot]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := c.Collect(context.Background(), Query{
				Org: "acme", Repos: []string{"platform"},
				IncludeTeams: tt.teams, IncludeAuthors: tt.authors,
				From: from, To: from.AddDate(0, 0, 7),
			})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, it := range items {
				got = append(got, it.Author)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("authors = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIncludeUnknownTeamFails(t *testing.T) {
	srv := fakeGitHub(t)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	_, err := c.Collect(context.Background(), Query{
		Org: "acme", Repos: []string{"platform"}, IncludeTeams: []string{"nope"},
		From: time.Now(), To: time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want an error naming the team", err)
	}
}
