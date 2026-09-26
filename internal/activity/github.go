package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Query struct {
	Org            string
	Repos          []string
	Topics         []string
	ExcludeAuthors []string
	ExcludeBots    bool // drop any author whose login ends in [bot]
	// IncludeTeams and IncludeAuthors, when either is set, keep only items by
	// members of those org teams (by slug) or by those logins.
	IncludeTeams   []string
	IncludeAuthors []string
	From, To       time.Time // [From, To)
}

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(token string) *Client {
	return &Client{BaseURL: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Token reads GITHUB_TOKEN, falling back to the gh CLI's stored login.
func Token() (string, error) {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t, nil
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("no GITHUB_TOKEN set and `gh auth token` failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Collect returns every PR and direct commit in the window, sorted by repo
// then time. Commits that belong to a collected PR are dropped.
func (c *Client) Collect(ctx context.Context, q Query) ([]Item, error) {
	include, err := c.includedAuthors(ctx, q)
	if err != nil {
		return nil, err
	}
	repos, err := c.repos(ctx, q)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, repo := range repos {
		prs, err := c.mergedPRs(ctx, q, repo)
		if err != nil {
			return nil, err
		}
		commits, err := c.directCommits(ctx, q, repo, prs)
		if err != nil {
			return nil, err
		}
		items = append(items, prs...)
		items = append(items, commits...)
	}
	return filterAndSort(items, include, q.ExcludeAuthors, q.ExcludeBots), nil
}

// includedAuthors returns the lowercased logins to keep, or nil when no
// include filter is set. GitHub logins are case-insensitive.
func (c *Client) includedAuthors(ctx context.Context, q Query) (map[string]bool, error) {
	if len(q.IncludeTeams) == 0 && len(q.IncludeAuthors) == 0 {
		return nil, nil
	}
	include := map[string]bool{}
	for _, login := range q.IncludeAuthors {
		include[strings.ToLower(login)] = true
	}
	for _, team := range q.IncludeTeams {
		path := fmt.Sprintf("/orgs/%s/teams/%s/members?per_page=100", q.Org, url.PathEscape(team))
		err := c.paginate(ctx, path, func(body []byte) error {
			var members []struct {
				Login string `json:"login"`
			}
			if err := json.Unmarshal(body, &members); err != nil {
				return err
			}
			for _, m := range members {
				include[strings.ToLower(m.Login)] = true
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("team %q members: %w", team, err)
		}
	}
	return include, nil
}

// repos merges the explicit list with repos found by topic.
func (c *Client) repos(ctx context.Context, q Query) ([]string, error) {
	repos := slices.Clone(q.Repos)
	for _, topic := range q.Topics {
		params := url.Values{"q": {fmt.Sprintf("org:%s topic:%s archived:false", q.Org, topic)}, "per_page": {"100"}}
		err := c.paginate(ctx, "/search/repositories?"+params.Encode(), func(body []byte) error {
			var res struct {
				Items []struct {
					Name string `json:"name"`
				} `json:"items"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return err
			}
			for _, r := range res.Items {
				repos = append(repos, r.Name)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(repos)
	return slices.Compact(repos), nil
}

func (c *Client) mergedPRs(ctx context.Context, q Query, repo string) ([]Item, error) {
	// merged: ranges are inclusive dates while To is exclusive, so the last
	// day is the one holding the final instant before To. To may fall mid-day
	// when the window is clamped to now.
	last := q.To.Add(-time.Nanosecond)
	merged := fmt.Sprintf("%s..%s", q.From.Format(time.DateOnly), last.Format(time.DateOnly))
	params := url.Values{
		"q":        {fmt.Sprintf("repo:%s/%s is:pr is:merged merged:%s", q.Org, repo, merged)},
		"per_page": {"100"},
	}
	var items []Item
	err := c.paginate(ctx, "/search/issues?"+params.Encode(), func(body []byte) error {
		var res struct {
			Items []struct {
				Number  int    `json:"number"`
				Title   string `json:"title"`
				Body    string `json:"body"`
				HTMLURL string `json:"html_url"`
				User    struct {
					Login string `json:"login"`
				} `json:"user"`
				Labels []struct {
					Name string `json:"name"`
				} `json:"labels"`
				PullRequest struct {
					MergedAt time.Time `json:"merged_at"`
				} `json:"pull_request"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return err
		}
		for _, pr := range res.Items {
			it := Item{
				Repo: repo, Kind: KindPR, Number: pr.Number, Title: pr.Title,
				Body: strings.TrimSpace(pr.Body), URL: pr.HTMLURL, Author: pr.User.Login,
				At: pr.PullRequest.MergedAt,
			}
			for _, l := range pr.Labels {
				it.Labels = append(it.Labels, l.Name)
			}
			items = append(items, it)
		}
		return nil
	})
	return items, err
}

var prRef = regexp.MustCompile(`#(\d+)`)

// directCommits lists default-branch commits that are not merge commits and
// do not reference one of the PRs already collected (squash merges do).
func (c *Client) directCommits(ctx context.Context, q Query, repo string, prs []Item) ([]Item, error) {
	seen := map[int]bool{}
	for _, pr := range prs {
		seen[pr.Number] = true
	}
	params := url.Values{
		"since":    {q.From.UTC().Format(time.RFC3339)},
		"until":    {q.To.UTC().Format(time.RFC3339)},
		"per_page": {"100"},
	}
	var items []Item
	err := c.paginate(ctx, fmt.Sprintf("/repos/%s/%s/commits?%s", q.Org, repo, params.Encode()), func(body []byte) error {
		var res []struct {
			HTMLURL string `json:"html_url"`
			Author  *struct {
				Login string `json:"login"`
			} `json:"author"`
			Parents []struct{} `json:"parents"`
			Commit  struct {
				Message string `json:"message"`
				Author  struct {
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return err
		}
		for _, cm := range res {
			if len(cm.Parents) > 1 {
				continue
			}
			title, rest, _ := strings.Cut(cm.Commit.Message, "\n")
			if referencesPR(title, seen) {
				continue
			}
			it := Item{
				Repo: repo, Kind: KindCommit, Title: strings.TrimSpace(title),
				Body: strings.TrimSpace(rest), URL: cm.HTMLURL, At: cm.Commit.Author.Date,
			}
			if cm.Author != nil {
				it.Author = cm.Author.Login
			}
			items = append(items, it)
		}
		return nil
	})
	return items, err
}

func referencesPR(title string, prs map[int]bool) bool {
	for _, m := range prRef.FindAllStringSubmatch(title, -1) {
		if n, _ := strconv.Atoi(m[1]); prs[n] {
			return true
		}
	}
	return false
}

// paginate follows Link rel="next" headers, handing each page body to fn.
func (c *Client) paginate(ctx context.Context, path string, fn func([]byte) error) error {
	h := http.Header{}
	h.Set("Accept", "application/vnd.github+json")
	h.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		h.Set("Authorization", "Bearer "+c.Token)
	}
	return getPages(ctx, c.HTTP, c.BaseURL+path, h, fn)
}
