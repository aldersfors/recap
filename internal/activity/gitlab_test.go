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

// fakeGitLab serves group acme/ops with project infra (explicit) and
// sub/tools (found by topic). Paths are matched escaped, so %2F matters.
func fakeGitLab(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "tok" {
			t.Errorf("PRIVATE-TOKEN = %q", got)
		}
		q := r.URL.Query()
		switch r.URL.EscapedPath() {
		case "/api/v4/groups/acme%2Fops/projects":
			if q.Get("topic") != "weekly" || q.Get("include_subgroups") != "true" || q.Get("archived") != "false" || q.Get("with_shared") != "false" {
				t.Errorf("project search query = %s", r.URL.RawQuery)
			}
			// other/ns/proj stands in for a project outside the group that a
			// server ignoring with_shared=false would still return.
			_, _ = fmt.Fprint(w, `[{"path_with_namespace":"acme/ops/infra"},{"path_with_namespace":"acme/ops/sub/tools"},{"path_with_namespace":"other/ns/proj"}]`)
		case "/api/v4/projects/acme%2Fops%2Finfra/merge_requests":
			if q.Get("page") == "" {
				if q.Get("state") != "merged" || q.Get("updated_after") != "2026-09-21T00:00:00Z" {
					t.Errorf("mr query = %s", r.URL.RawQuery)
				}
				w.Header().Set("Link", fmt.Sprintf(`<%s/api/v4/projects/acme%%2Fops%%2Finfra/merge_requests?page=2>; rel="next"`, srv.URL))
				_, _ = fmt.Fprint(w, `[
				  {"iid":5,"title":"Add Grafana SSO","description":"Uses Keycloak","web_url":"https://gl/mr/5","author":{"username":"Alice"},"labels":["feature"],"merged_at":"2026-09-22T10:00:00Z","merge_commit_sha":"m5","squash_commit_sha":"s5"},
				  {"iid":4,"title":"Merged last week","web_url":"https://gl/mr/4","author":{"username":"alice"},"merged_at":"2026-09-18T10:00:00Z"},
				  {"iid":3,"title":"Imported, no merge time","web_url":"https://gl/mr/3","author":{"username":"alice"},"merged_at":null},
				  {"iid":8,"title":"Merged at window start","web_url":"https://gl/mr/8","author":{"username":"alice"},"merged_at":"2026-09-21T00:00:00Z"}
				]`)
				return
			}
			_, _ = fmt.Fprint(w, `[
			  {"iid":6,"title":"Merged at window end","web_url":"https://gl/mr/6","author":{"username":"alice"},"merged_at":"2026-09-28T00:00:00Z"},
			  {"iid":7,"title":"Bump chart","web_url":"https://gl/mr/7","author":{"username":"project_42_bot_abc"},"merged_at":"2026-09-27T23:00:00Z"}
			]`)
		case "/api/v4/projects/acme%2Fops%2Finfra/merge_requests/5/commits":
			_, _ = fmt.Fprint(w, `[{"id":"c5a"}]`)
		case "/api/v4/projects/acme%2Fops%2Finfra/merge_requests/7/commits",
			"/api/v4/projects/acme%2Fops%2Finfra/merge_requests/8/commits",
			"/api/v4/projects/acme%2Fops%2Fsub%2Ftools/merge_requests",
			"/api/v4/projects/acme%2Fops%2Fsub%2Ftools/repository/commits":
			_, _ = fmt.Fprint(w, `[]`)
		case "/api/v4/projects/acme%2Fops%2Finfra/repository/commits":
			if q.Get("since") != "2026-09-21T00:00:00Z" || q.Get("until") != "2026-09-28T00:00:00Z" || q.Get("first_parent") != "true" {
				t.Errorf("commits query = %s", r.URL.RawQuery)
			}
			_, _ = fmt.Fprint(w, `[
			  {"id":"c5a","message":"Part of MR 5","web_url":"https://gl/c/c5a","author_email":"alice@corp.example","authored_date":"2026-09-22T09:00:00Z","parent_ids":["p"]},
			  {"id":"s5","message":"Add Grafana SSO","web_url":"https://gl/c/s5","author_email":"alice@corp.example","authored_date":"2026-09-22T10:00:00Z","parent_ids":["p"]},
			  {"id":"m5","message":"Merge branch 'sso' into 'main'","web_url":"https://gl/c/m5","author_email":"alice@corp.example","authored_date":"2026-09-22T10:00:01Z","parent_ids":["p","q"]},
			  {"id":"d1","message":"Rotate backup key\n\nYearly rotation.","web_url":"https://gl/c/d1","author_email":"Bob@Corp.example","authored_date":"2026-09-24T09:00:00Z","parent_ids":["p"]}
			]`)
		case "/api/v4/groups/acme%2Fops%2Fsre/members/all":
			// Includes ancestor-group members, which include_groups must not count.
			_, _ = fmt.Fprint(w, `[{"username":"alice"},{"username":"project_42_bot_abc"}]`)
		case "/api/v4/groups/acme%2Fops%2Fsre/members":
			_, _ = fmt.Fprint(w, `[{"username":"alice"}]`)
		case "/api/v4/groups/acme%2Fops%2Fsre/descendant_groups":
			_, _ = fmt.Fprint(w, `[{"full_path":"acme/ops/sre/oncall"}]`)
		case "/api/v4/groups/acme%2Fops%2Fsre%2Foncall/members":
			_, _ = fmt.Fprint(w, `[{"username":"Carol"}]`)
		default:
			http.Error(w, `{"message":"404 Not found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

var glFrom = time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)

// collectGitLab runs q against the fake with the shared group, projects,
// topic and window filled in, and returns "repo kind number author" lines.
func collectGitLab(t *testing.T, q GitLabQuery) ([]Item, []string) {
	t.Helper()
	srv := fakeGitLab(t)
	c := NewGitLab(srv.URL+"/", "tok") // trailing slash must be tolerated
	c.HTTP = srv.Client()
	q.Group, q.Projects, q.Topics = "acme/ops", []string{"infra"}, []string{"weekly"}
	q.From, q.To = glFrom, glFrom.AddDate(0, 0, 7)
	items, err := c.Collect(context.Background(), q)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var lines []string
	for _, it := range items {
		lines = append(lines, fmt.Sprintf("%s %s %d %s", it.Repo, it.Kind, it.Number, it.Author))
	}
	return items, lines
}

func TestGitLabMergedMRs(t *testing.T) {
	items, lines := collectGitLab(t, GitLabQuery{})
	want := []string{
		"infra mr 8 alice",
		"infra mr 5 Alice",
		"infra commit 0 Bob@Corp.example",
		"infra mr 7 project_42_bot_abc",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("items:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	mr := items[1]
	if mr.Title != "Add Grafana SSO" || mr.Body != "Uses Keycloak" || mr.URL != "https://gl/mr/5" || len(mr.Labels) != 1 || mr.Labels[0] != "feature" {
		t.Errorf("mr item = %+v", mr)
	}
	if !mr.At.Equal(time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("mr At = %v, want merged_at", mr.At)
	}
}

func TestGitLabBaseURLWithPathPrefix(t *testing.T) {
	var first string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first == "" {
			first = r.URL.EscapedPath()
		}
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	c := NewGitLab(srv.URL+"/gitlab/", "")
	c.HTTP = srv.Client()
	if _, err := c.Collect(context.Background(), GitLabQuery{Group: "g", Projects: []string{"p"}, From: glFrom, To: glFrom}); err != nil {
		t.Fatal(err)
	}
	if want := "/gitlab/api/v4/projects/g%2Fp/merge_requests"; first != want {
		t.Errorf("first request = %q, want %q", first, want)
	}
}

func TestGitLabHTTPErrorNamesPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"404 Project Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewGitLab(srv.URL, "tok")
	c.HTTP = srv.Client()
	_, err := c.Collect(context.Background(), GitLabQuery{Group: "acme/ops", Projects: []string{"gone"}, From: glFrom, To: glFrom})
	if err == nil || !strings.Contains(err.Error(), "acme/ops/gone") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want 404 naming acme/ops/gone", err)
	}
}

func TestGitLabDropsCommitsThatBelongToMRs(t *testing.T) {
	items, _ := collectGitLab(t, GitLabQuery{})
	var commits []string
	for _, it := range items {
		if it.Kind == KindCommit {
			commits = append(commits, it.URL)
		}
	}
	// c5a is an MR commit, s5 the squash commit, m5 a merge commit.
	if len(commits) != 1 || commits[0] != "https://gl/c/d1" {
		t.Fatalf("commits = %v, want only the direct commit d1", commits)
	}
	if items[2].Title != "Rotate backup key" || items[2].Body != "Yearly rotation." {
		t.Errorf("commit item = %+v", items[2])
	}
}

func TestGitLabFilters(t *testing.T) {
	tests := []struct {
		name string
		q    GitLabQuery
		want []string
	}{
		{"exclude bots", GitLabQuery{ExcludeBots: true},
			[]string{"infra mr 8 alice", "infra mr 5 Alice", "infra commit 0 Bob@Corp.example"}},
		{"include group, case-insensitive", GitLabQuery{IncludeGroups: []string{"sre"}},
			[]string{"infra mr 8 alice", "infra mr 5 Alice"}},
		{"include group plus commit email", GitLabQuery{IncludeGroups: []string{"sre"}, IncludeAuthors: []string{"bob@corp.example"}},
			[]string{"infra mr 8 alice", "infra mr 5 Alice", "infra commit 0 Bob@Corp.example"}},
		{"configured email in mixed case", GitLabQuery{IncludeAuthors: []string{"BOB@corp.EXAMPLE"}},
			[]string{"infra commit 0 Bob@Corp.example"}},
		{"exclude author, case-insensitive", GitLabQuery{ExcludeAuthors: []string{"alice", "bob@corp.example"}},
			[]string{"infra mr 7 project_42_bot_abc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := collectGitLab(t, tt.q)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("items:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestGitLabUnknownIncludeGroupFails(t *testing.T) {
	srv := fakeGitLab(t)
	c := NewGitLab(srv.URL, "tok")
	c.HTTP = srv.Client()
	_, err := c.Collect(context.Background(), GitLabQuery{
		Group: "acme/ops", Projects: []string{"infra"}, IncludeGroups: []string{"nope"},
		From: glFrom, To: glFrom.AddDate(0, 0, 7),
	})
	if err == nil || !strings.Contains(err.Error(), "acme/ops/nope") {
		t.Fatalf("err = %v, want an error naming acme/ops/nope", err)
	}
}

func TestGitLabIncludeGroupsCountsDescendantsNotAncestors(t *testing.T) {
	srv := fakeGitLab(t)
	c := NewGitLab(srv.URL, "tok")
	c.HTTP = srv.Client()
	got, err := c.includedAuthors(context.Background(), GitLabQuery{Group: "acme/ops", IncludeGroups: []string{"sre"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["alice"] || !got["carol"] {
		t.Errorf("include set = %v, want alice and carol (direct and subgroup members, no ancestors)", got)
	}
}
