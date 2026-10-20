package activity

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// PlaneQuery selects the completed work items Plane.Completed gathers.
type PlaneQuery struct {
	Projects       []string // project identifiers, e.g. ENG
	IncludeMembers []string // member emails whose work counts
	From, To       time.Time
}

// PlaneItem is a completed work item: the activity item plus whether it is an
// epic, for the stats line.
type PlaneItem struct {
	Item
	Epic bool
}

// Plane talks to the REST API v1 of one Plane workspace.
type Plane struct {
	BaseURL   string // instance root, e.g. https://plane.example.com
	Workspace string // workspace slug
	Token     string
	HTTP      *http.Client
}

func NewPlane(baseURL, workspace, token string) *Plane {
	return &Plane{BaseURL: strings.TrimSuffix(baseURL, "/"), Workspace: workspace, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// PlaneToken reads PLANE_API_KEY.
func PlaneToken() (string, error) {
	if t := os.Getenv("PLANE_API_KEY"); t != "" {
		return t, nil
	}
	return "", errors.New("no PLANE_API_KEY set: create a personal access token in Plane under profile settings")
}

// maxPlanePages bounds a cursor walk, so a server that keeps answering
// next_page_results cannot loop forever.
const maxPlanePages = 500

// planeType and planeState read a field that Plane returns as an object with
// expand= and as a bare id without it; the bare id leaves them empty.
type planeType struct {
	Name   string `json:"name"`
	IsEpic bool   `json:"is_epic"`
}

func (t *planeType) UnmarshalJSON(b []byte) error {
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return nil
	}
	type plain planeType
	return json.Unmarshal(b, (*plain)(t))
}

func (t planeType) epic() bool { return t.IsEpic || t.Name == "Epic" }

type planeState struct {
	Group string `json:"group"`
}

func (s *planeState) UnmarshalJSON(b []byte) error {
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return nil
	}
	type plain planeState
	return json.Unmarshal(b, (*plain)(s))
}

type workItem struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description_stripped"`
	SequenceID  int        `json:"sequence_id"`
	Project     string     `json:"project"`
	Parent      string     `json:"parent"`
	Assignees   []string   `json:"assignees"`
	CompletedAt *time.Time `json:"completed_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	State       planeState `json:"state"`
	Type        planeType  `json:"type"`
}

// Completed returns the work items in q.Projects whose state is in the
// completed group and that completed in [From, To), assigned to a member of
// q.IncludeMembers, sorted by project then time. Cancelled, unassigned and
// archived items are left out.
func (p *Plane) Completed(ctx context.Context, q PlaneQuery) ([]PlaneItem, error) {
	projects, err := p.projects(ctx)
	if err != nil {
		return nil, err
	}
	include, err := p.members(ctx, q.IncludeMembers)
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for id, ident := range projects {
		ids[strings.ToUpper(ident)] = id
	}
	var items []PlaneItem
	for _, ident := range q.Projects {
		ident = strings.ToUpper(ident)
		id, ok := ids[ident]
		if !ok {
			return nil, fmt.Errorf("plane project %q not found in workspace %s", ident, p.Workspace)
		}
		params := url.Values{"expand": {"state,type"}, "order_by": {"-updated_at"}, "per_page": {"100"}}
		err := p.pages(ctx, fmt.Sprintf("/projects/%s/work-items/", id), params, func(raw json.RawMessage) (bool, error) {
			var res []workItem
			if err := json.Unmarshal(raw, &res); err != nil {
				return false, err
			}
			for _, w := range res {
				// Completing an item updates it, so once updated_at falls
				// before the window, no later item can have completed in it.
				if w.UpdatedAt.Before(q.From) {
					return false, nil
				}
				if w.State.Group != "completed" || w.CompletedAt == nil || w.CompletedAt.Before(q.From) || !w.CompletedAt.Before(q.To) {
					continue
				}
				author := ""
				for _, a := range w.Assignees {
					if name, ok := include[a]; ok {
						author = name
						break
					}
				}
				if author == "" {
					continue
				}
				ref := fmt.Sprintf("%s-%d", ident, w.SequenceID)
				items = append(items, PlaneItem{Epic: w.Type.epic(), Item: Item{
					Repo: ident, Kind: KindTicket, Number: w.SequenceID, Title: w.Name,
					Body: strings.TrimSpace(w.Description), URL: p.browseURL(ref), Author: author,
					At: *w.CompletedAt,
				}})
			}
			return true, nil
		})
		if err != nil {
			return nil, fmt.Errorf("plane project %s: %w", ident, err)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Repo != items[j].Repo {
			return items[i].Repo < items[j].Repo
		}
		return items[i].At.Before(items[j].At)
	})
	return items, nil
}

// PlaneStats counts the completed work items: epics apart from the rest.
func PlaneStats(items []PlaneItem) TicketStats {
	var s TicketStats
	for _, it := range items {
		if it.Epic {
			s.Epics++
		} else {
			s.Tickets++
		}
	}
	return s
}

// Epics resolves each reference, such as ENG-12, to its epic: its nearest
// ancestor that is an epic, or the topmost ancestor when none is. A reference
// that cannot be fetched is left out, as with GitHub tickets.
func (p *Plane) Epics(ctx context.Context, refs []string) map[string]Epic {
	epics := map[string]Epic{}
	projects, err := p.projects(ctx)
	if err != nil {
		return epics
	}
	cache := map[string]workItem{}
	for _, ref := range refs {
		node, err := p.workItem(ctx, "/work-items/"+url.PathEscape(ref)+"/", nil)
		if err != nil {
			continue
		}
		for depth := 0; !node.Type.epic() && node.Parent != "" && depth < maxParentDepth; depth++ {
			path := fmt.Sprintf("/projects/%s/work-items/%s/", node.Project, node.Parent)
			parent, err := p.workItem(ctx, path, cache)
			if err != nil {
				break
			}
			node = parent
		}
		ident, ok := projects[node.Project]
		if !ok {
			continue
		}
		epics[ref] = Epic{Ref: fmt.Sprintf("%s-%d", strings.ToUpper(ident), node.SequenceID), Title: node.Name}
	}
	return epics
}

func (p *Plane) workItem(ctx context.Context, path string, cache map[string]workItem) (workItem, error) {
	if w, ok := cache[path]; ok {
		return w, nil
	}
	body, err := p.get(ctx, path, url.Values{"expand": {"type"}})
	if err != nil {
		return workItem{}, err
	}
	var w workItem
	if err := json.Unmarshal(body, &w); err != nil {
		return workItem{}, fmt.Errorf("GET %s: decode: %w", path, err)
	}
	if cache != nil {
		cache[path] = w
	}
	return w, nil
}

// projects maps every project id in the workspace to its identifier.
func (p *Plane) projects(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	err := p.pages(ctx, "/projects/", url.Values{"per_page": {"100"}}, func(raw json.RawMessage) (bool, error) {
		var res []struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return false, err
		}
		for _, r := range res {
			out[r.ID] = r.Identifier
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("plane projects: %w", err)
	}
	return out, nil
}

// members maps the ids of the workspace members with one of emails
// (case-insensitive) to their display names.
func (p *Plane) members(ctx context.Context, emails []string) (map[string]string, error) {
	want := map[string]bool{}
	for _, e := range emails {
		want[strings.ToLower(e)] = true
	}
	out := map[string]string{}
	err := p.pages(ctx, "/members/", url.Values{"per_page": {"100"}}, func(raw json.RawMessage) (bool, error) {
		var res []struct {
			ID          string `json:"id"`
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return false, err
		}
		for _, m := range res {
			if want[strings.ToLower(m.Email)] {
				out[m.ID] = cmp.Or(m.DisplayName, m.Email)
			}
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("plane members: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("none of plane.include_members is a member of workspace %s", p.Workspace)
	}
	return out, nil
}

func (p *Plane) browseURL(ref string) string {
	return fmt.Sprintf("%s/%s/browse/%s/", p.BaseURL, p.Workspace, ref)
}

// pages walks Plane's cursor pagination, handing each page's results to fn
// until fn returns false or there is no next page. An endpoint that answers
// with a bare list is one page.
func (p *Plane) pages(ctx context.Context, path string, params url.Values, fn func(json.RawMessage) (bool, error)) error {
	params = maps.Clone(params) // Set below replaces values, never edits them
	seen := map[string]bool{}
	for range maxPlanePages {
		body, err := p.get(ctx, path, params)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
			_, err := fn(body)
			return err
		}
		var page struct {
			Results         json.RawMessage `json:"results"`
			NextCursor      string          `json:"next_cursor"`
			NextPageResults bool            `json:"next_page_results"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("GET %s: decode: %w", path, err)
		}
		more, err := fn(page.Results)
		if err != nil {
			return fmt.Errorf("GET %s: decode: %w", path, err)
		}
		if !more || !page.NextPageResults || page.NextCursor == "" || seen[page.NextCursor] {
			return nil
		}
		seen[page.NextCursor] = true
		params.Set("cursor", page.NextCursor)
	}
	return fmt.Errorf("GET %s: more than %d pages", path, maxPlanePages)
}

func (p *Plane) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := p.BaseURL + "/api/v1/workspaces/" + url.PathEscape(p.Workspace) + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if p.Token != "" {
		req.Header.Set("X-API-Key", p.Token)
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %d %s", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
