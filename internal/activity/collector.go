// Package activity collects what the team shipped in a time window: merged
// pull or merge requests, plus commits pushed straight to the default branch,
// from GitHub or GitLab.
package activity

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	KindPR     = "pr"
	KindMR     = "mr"
	KindCommit = "commit"
)

type Item struct {
	Repo   string    `json:"repo"`
	Kind   string    `json:"kind"`
	Number int       `json:"number,omitempty"`
	Title  string    `json:"title"`
	Body   string    `json:"body,omitempty"`
	URL    string    `json:"url"`
	Author string    `json:"author,omitempty"`
	Labels []string  `json:"labels,omitempty"`
	At     time.Time `json:"at"`
}

// Collector gathers the items in [from, to) from one forge.
type Collector interface {
	Collect(ctx context.Context, from, to time.Time) ([]Item, error)
}

// GitHubCollector runs Query against Client for the given window.
type GitHubCollector struct {
	Client *Client
	Query  Query
}

func (g GitHubCollector) Collect(ctx context.Context, from, to time.Time) ([]Item, error) {
	q := g.Query
	q.From, q.To = from, to
	return g.Client.Collect(ctx, q)
}

var gitlabBot = regexp.MustCompile(`^(project|group)_\d+_bot`)

// isBot reports GitHub app logins and GitLab project or group access-token users.
func isBot(login string) bool {
	return strings.HasSuffix(login, "[bot]") || gitlabBot.MatchString(login)
}

// filterAndSort keeps items whose author is in include (lowercased; nil keeps
// everyone), drops excluded authors (case-insensitive) and optionally bots,
// then sorts by repo and time. Logins and emails are both case-insensitive.
func filterAndSort(items []Item, include map[string]bool, exclude []string, excludeBots bool) []Item {
	excluded := map[string]bool{}
	for _, a := range exclude {
		excluded[strings.ToLower(a)] = true
	}
	items = slices.DeleteFunc(items, func(it Item) bool {
		author := strings.ToLower(it.Author)
		if include != nil && !include[author] {
			return true
		}
		return excluded[author] || (excludeBots && isBot(it.Author))
	})
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Repo != items[j].Repo {
			return items[i].Repo < items[j].Repo
		}
		return items[i].At.Before(items[j].At)
	})
	return items
}

// ForStorage returns a copy of items fit for activity.json: email authors
// (GitLab commits) are dropped, logins are kept for checking the draft.
func ForStorage(items []Item) []Item {
	out := slices.Clone(items)
	for i := range out {
		if strings.Contains(out[i].Author, "@") {
			out[i].Author = ""
		}
	}
	return out
}

// getPages GETs next and every Link rel="next" page after it, handing each
// body to fn. Both forges paginate with the same Link header. A next link on
// another scheme or host is refused, since header carries the token.
func getPages(ctx context.Context, hc *http.Client, next string, header http.Header, fn func([]byte) error) error {
	var origin *url.URL
	for next != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return err
		}
		if origin == nil {
			origin = req.URL
		} else if req.URL.Scheme != origin.Scheme || req.URL.Host != origin.Host {
			return fmt.Errorf("GET %s: refusing to follow a next link to another host (%s://%s)", origin.Path, req.URL.Scheme, req.URL.Host)
		}
		maps.Copy(req.Header, header)
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: %d %s", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if err := fn(body); err != nil {
			return fmt.Errorf("GET %s: decode: %w", req.URL.Path, err)
		}
		next = nextLink(resp.Header.Get("Link"))
	}
	return nil
}

func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		target, params, ok := strings.Cut(part, ";")
		if ok && strings.Contains(params, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(target), "<>")
		}
	}
	return ""
}
