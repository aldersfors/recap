package activity

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestEpics(t *testing.T) {
	var srv *httptest.Server
	calls := map[string]int{}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		parent := func(n int) string {
			return fmt.Sprintf(`,"parent_issue_url":"%s/repos/acme/project/issues/%d"`, srv.URL, n)
		}
		switch r.URL.Path {
		case "/repos/acme/project/issues/11": // task under a story under an epic
			_, _ = fmt.Fprintf(w, `{"number":11,"title":"Task one","type":{"name":"Task"}%s}`, parent(10))
		case "/repos/acme/project/issues/12": // second task under the same story
			_, _ = fmt.Fprintf(w, `{"number":12,"title":"Task two","type":{"name":"Task"}%s}`, parent(10))
		case "/repos/acme/project/issues/10":
			_, _ = fmt.Fprintf(w, `{"number":10,"title":"Story","type":{"name":"Story"}%s}`, parent(1))
		case "/repos/acme/project/issues/1":
			_, _ = fmt.Fprintf(w, `{"number":1,"title":"Parent epic","type":{"name":"Epic"}%s}`, parent(100))
		case "/repos/acme/project/issues/20": // untyped, no parent: its own root
			_, _ = fmt.Fprint(w, `{"number":20,"title":"Standalone task","type":null}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}

	got := c.Epics(context.Background(), "acme", "project", []int{11, 12, 1, 20, 404})
	want := map[int]Epic{
		11: {Ref: "acme/project#1", Title: "Parent epic"},
		12: {Ref: "acme/project#1", Title: "Parent epic"},
		1:  {Ref: "acme/project#1", Title: "Parent epic"},
		20: {Ref: "acme/project#20", Title: "Standalone task"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epics = %v, want %v", got, want)
	}
	// The walk stops at the Epic, never fetching its parent, and each issue is
	// fetched once however many tickets share it.
	if calls["/repos/acme/project/issues/100"] != 0 {
		t.Error("walked past the Epic")
	}
	if calls["/repos/acme/project/issues/10"] != 1 {
		t.Errorf("shared parent fetched %d times, want 1", calls["/repos/acme/project/issues/10"])
	}
}

func TestAPIPathDropsEnterprisePrefix(t *testing.T) {
	c := &Client{BaseURL: "https://ghe.example.com/api/v3"}
	if got := c.apiPath("https://ghe.example.com/api/v3/repos/acme/project/issues/7"); got != "/repos/acme/project/issues/7" {
		t.Errorf("apiPath = %q", got)
	}
	if got := c.apiPath("https://ghe.example.com/api/v3/orgs/acme"); got != "" {
		t.Errorf("apiPath of a non-issue URL = %q, want empty", got)
	}
}
