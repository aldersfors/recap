package activity

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const planeBase = "/api/v1/workspaces/acme"

// fakePlane serves two projects. ENG lists its work items newest-updated
// first over two cursor pages, with an item on the second page updated before
// the window to check that the walk stops there. calls counts each path.
func fakePlane(t *testing.T) (*Plane, map[string]int) {
	t.Helper()
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path+"?"+r.URL.Query().Get("cursor")]++
		if r.Header.Get("X-API-Key") != "tok" {
			t.Errorf("%s: X-API-Key = %q", r.URL.Path, r.Header.Get("X-API-Key"))
		}
		item := func(seq int, name, updated, completed, group, typ, parent string, assignees ...string) string {
			if updated == "" {
				updated = "2026-09-01T00:00:00Z"
			}
			c := "null"
			if completed != "" {
				c = `"` + completed + `"`
			}
			p := "null"
			if parent != "" {
				p = `"` + parent + `"`
			}
			return fmt.Sprintf(`{"id":"eng-%d","name":%q,"description_stripped":"Body %d","sequence_id":%d,"project":"p-eng",
			  "parent":%s,"assignees":["%s"],"completed_at":%s,"updated_at":%q,"state":{"group":%q},"type":%s}`,
				seq, name, seq, seq, p, strings.Join(assignees, `","`), c, updated, group, typ)
		}
		task, epic := `{"name":"Task","is_epic":false}`, `{"name":"Epic","is_epic":true}`
		switch r.URL.Path {
		case planeBase + "/projects/":
			_, _ = fmt.Fprint(w, `{"results":[{"id":"p-eng","identifier":"ENG"},{"id":"p-ops","identifier":"OPS"}],"next_page_results":false}`)
		case planeBase + "/members/": // a bare list, as some versions answer
			_, _ = fmt.Fprint(w, `[{"id":"u-alice","email":"Alice@example.com","display_name":"alice"},{"id":"u-bob","email":"bob@example.com","display_name":"bob"}]`)
		case planeBase + "/projects/p-eng/work-items/":
			if q := r.URL.Query(); q.Get("order_by") != "-updated_at" || q.Get("expand") != "state,type" {
				t.Errorf("work-items query = %v", q)
			}
			switch r.URL.Query().Get("cursor") {
			case "":
				_, _ = fmt.Fprintf(w, `{"results":[%s,%s,%s,%s],"next_cursor":"100:1:0","next_page_results":true}`,
					item(5, "Done after the window", "2026-09-28T09:00:00Z", "2026-09-28T09:00:00Z", "completed", task, "", "u-alice"),
					item(4, "Ship the widget", "2026-09-27T23:00:00Z", "2026-09-27T23:00:00Z", "completed", task, "", "u-bob", "u-alice"),
					item(3, "Cancelled", "2026-09-25T10:00:00Z", "", "cancelled", task, "", "u-alice"),
					item(2, "Someone else's", "2026-09-24T10:00:00Z", "2026-09-24T10:00:00Z", "completed", task, "", "u-carol"),
				)
			case "100:1:0":
				_, _ = fmt.Fprintf(w, `{"results":[%s,%s],"next_cursor":"100:2:0","next_page_results":true}`,
					item(1, "Widget epic", "2026-09-21T00:00:00Z", "2026-09-21T00:00:00Z", "completed", epic, "", "u-alice"),
					item(0, "Older", "2026-09-20T23:59:59Z", "2026-09-20T23:59:59Z", "completed", task, "", "u-alice"),
				)
			default:
				t.Error("walked past an item updated before the window")
				_, _ = fmt.Fprint(w, `{"results":[]}`)
			}
		case planeBase + "/work-items/ENG-11/": // task under a story under an epic
			_, _ = fmt.Fprint(w, item(11, "Task", "", "", "started", task, "eng-10"))
		case planeBase + "/work-items/ENG-12/":
			_, _ = fmt.Fprint(w, item(12, "Task two", "", "", "started", task, "eng-10"))
		case planeBase + "/projects/p-eng/work-items/eng-10/":
			_, _ = fmt.Fprint(w, item(10, "Story", "", "", "started", `"type-uuid"`, "eng-1"))
		case planeBase + "/projects/p-eng/work-items/eng-1/":
			_, _ = fmt.Fprint(w, item(1, "Widget epic", "", "", "started", epic, "eng-100"))
		case planeBase + "/work-items/ENG-20/": // no type, no parent: its own root
			_, _ = fmt.Fprint(w, item(20, "Standalone", "", "", "started", "null", ""))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Plane{BaseURL: srv.URL, Workspace: "acme", Token: "tok", HTTP: srv.Client()}, calls
}

func TestPlaneCompleted(t *testing.T) {
	p, _ := fakePlane(t)
	got, err := p.Completed(context.Background(), PlaneQuery{
		Projects: []string{"eng"}, IncludeMembers: []string{"alice@EXAMPLE.com"},
		From: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, it := range got {
		refs = append(refs, fmt.Sprintf("%s-%d %s epic=%v", it.Repo, it.Number, it.Author, it.Epic))
	}
	// Outside the window, cancelled and others' items are dropped; the window
	// start is inclusive and its end exclusive.
	want := []string{"ENG-1 alice epic=true", "ENG-4 alice epic=false"}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("Completed = %v, want %v", refs, want)
	}
	it := got[1].Item
	if it.Kind != KindTicket || it.Title != "Ship the widget" || it.Body != "Body 4" || !strings.HasSuffix(it.URL, "/acme/browse/ENG-4/") {
		t.Errorf("item = %+v", it)
	}
	if s := PlaneStats(got); s != (TicketStats{Tickets: 1, Epics: 1}) {
		t.Errorf("PlaneStats = %+v", s)
	}
}

func TestPlaneCompletedErrors(t *testing.T) {
	p, _ := fakePlane(t)
	q := PlaneQuery{Projects: []string{"NOPE"}, IncludeMembers: []string{"alice@example.com"}}
	if _, err := p.Completed(context.Background(), q); err == nil || !strings.Contains(err.Error(), `"NOPE" not found`) {
		t.Errorf("unknown project: err = %v", err)
	}
	q = PlaneQuery{Projects: []string{"ENG"}, IncludeMembers: []string{"dave@example.com"}}
	if _, err := p.Completed(context.Background(), q); err == nil || !strings.Contains(err.Error(), "include_members") {
		t.Errorf("no matching member: err = %v", err)
	}
}

func TestPlaneEpics(t *testing.T) {
	p, calls := fakePlane(t)
	got := p.Epics(context.Background(), []string{"ENG-11", "ENG-12", "ENG-20", "ENG-404"})
	want := map[string]Epic{
		"ENG-11": {Ref: "ENG-1", Title: "Widget epic"},
		"ENG-12": {Ref: "ENG-1", Title: "Widget epic"},
		"ENG-20": {Ref: "ENG-20", Title: "Standalone"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epics = %v, want %v", got, want)
	}
	// The walk stops at the epic, and a shared parent is fetched once. A type
	// given as a bare id (not expanded) is read as not an epic.
	if calls[planeBase+"/projects/p-eng/work-items/eng-100/?"] != 0 {
		t.Error("walked past the epic")
	}
	if n := calls[planeBase+"/projects/p-eng/work-items/eng-10/?"]; n != 1 {
		t.Errorf("shared parent fetched %d times, want 1", n)
	}
}
