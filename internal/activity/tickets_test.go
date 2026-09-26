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

func fakeTickets(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/orgs/acme/teams/ops/members":
			_, _ = fmt.Fprint(w, `[{"login":"alice"}]`)
		case "/search/issues":
			if r.URL.Query().Get("page") == "" {
				if q := r.URL.Query().Get("q"); q != "repo:acme/project is:issue is:closed closed:2026-09-21..2026-09-27" {
					t.Errorf("q = %q", q)
				}
				w.Header().Set("Link", fmt.Sprintf(`<%s/search/issues?page=2>; rel="next"`, srv.URL))
				_, _ = fmt.Fprint(w, `{"items":[
				  {"number":1,"state_reason":"completed","type":{"name":"Task"},"assignees":[{"login":"Alice"}]},
				  {"number":2,"state_reason":"completed","type":{"name":"Epic"},"assignees":[{"login":"bob"},{"login":"alice"}]},
				  {"number":3,"state_reason":"not_planned","type":{"name":"Task"},"assignees":[{"login":"alice"}]}
				]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"items":[
			  {"number":4,"state_reason":"completed","type":null,"assignees":[{"login":"alice"}]},
			  {"number":5,"state_reason":"completed","type":{"name":"Bug"},"assignees":[{"login":"bob"}]},
			  {"number":6,"state_reason":"completed","type":{"name":"Task"},"assignees":[]}
			]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClosedTickets(t *testing.T) {
	srv := fakeTickets(t)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	from := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		q    Query
		want TicketStats
	}{
		// Only issues assigned to an included member count: never unassigned or
		// not-planned ones. Assignees match case-insensitively.
		{"team filter", Query{IncludeTeams: []string{"ops"}}, TicketStats{Tickets: 2, Epics: 1}},
		{"author filter", Query{IncludeAuthors: []string{"BOB"}}, TicketStats{Tickets: 1, Epics: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.q
			q.Org, q.From, q.To = "acme", from, from.AddDate(0, 0, 7)
			got, err := c.ClosedTickets(context.Background(), q, "project")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("ClosedTickets = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestClosedTicketsReportsErrors(t *testing.T) {
	srv := fakeTickets(t)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	_, err := c.ClosedTickets(context.Background(), Query{Org: "acme", IncludeTeams: []string{"nope"}, From: time.Now(), To: time.Now()}, "project")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want an error naming the team", err)
	}
}

func TestTicketStatsLine(t *testing.T) {
	for _, tt := range []struct {
		s     TicketStats
		weeks int
		want  string
	}{
		{TicketStats{Tickets: 85, Epics: 11}, 2, "*Closed this period: 85 tickets, 11 epics.*"},
		{TicketStats{Tickets: 1, Epics: 1}, 1, "*Closed this week: 1 ticket, 1 epic.*"},
		{TicketStats{}, 1, "*Closed this week: 0 tickets, 0 epics.*"},
	} {
		if got := tt.s.Line(tt.weeks); got != tt.want {
			t.Errorf("Line(%d) = %q, want %q", tt.weeks, got, tt.want)
		}
	}
}

func TestClosedTicketsNeedsATeam(t *testing.T) {
	srv := fakeTickets(t)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	_, err := c.ClosedTickets(context.Background(), Query{Org: "acme", From: time.Now(), To: time.Now()}, "project")
	if err == nil || !strings.Contains(err.Error(), "include_teams") {
		t.Fatalf("err = %v, want a refusal to count without a team filter", err)
	}
}
