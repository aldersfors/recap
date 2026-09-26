package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// GitLabQuery selects what GitLab.Collect gathers. Projects and IncludeGroups
// are paths relative to Group.
type GitLabQuery struct {
	Group          string
	Projects       []string
	Topics         []string
	IncludeGroups  []string
	IncludeAuthors []string // usernames, or commit author emails
	ExcludeAuthors []string
	ExcludeBots    bool
	From, To       time.Time // [From, To)
}

// GitLab talks to the REST API v4 of one GitLab instance.
type GitLab struct {
	BaseURL string // instance root, e.g. https://gitlab.com or https://git.corp/gitlab
	Token   string
	HTTP    *http.Client
}

func NewGitLab(baseURL, token string) *GitLab {
	return &GitLab{BaseURL: strings.TrimSuffix(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// GitLabCollector runs Query against Client for the given window.
type GitLabCollector struct {
	Client *GitLab
	Query  GitLabQuery
}

func (g GitLabCollector) Collect(ctx context.Context, from, to time.Time) ([]Item, error) {
	q := g.Query
	q.From, q.To = from, to
	return g.Client.Collect(ctx, q)
}

// GitLabToken reads GITLAB_TOKEN, falling back to glab's stored token for host.
func GitLabToken(host string) (string, error) {
	if t := os.Getenv("GITLAB_TOKEN"); t != "" {
		return t, nil
	}
	out, err := exec.Command("glab", "config", "get", "token", "--host", host).Output()
	if err != nil {
		return "", fmt.Errorf("no GITLAB_TOKEN set and `glab config get token --host %s` failed: %w", host, err)
	}
	t := strings.TrimSpace(string(out))
	if t == "" {
		return "", fmt.Errorf("no GITLAB_TOKEN set and glab has no token for %s", host)
	}
	return t, nil
}

// Collect returns every merged MR and direct default-branch commit in the
// window, sorted by project then time. Commits that belong to a collected MR
// are dropped.
func (c *GitLab) Collect(ctx context.Context, q GitLabQuery) ([]Item, error) {
	include, err := c.includedAuthors(ctx, q)
	if err != nil {
		return nil, err
	}
	projects, err := c.projects(ctx, q)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, p := range projects {
		mrs, inMR, err := c.mergedMRs(ctx, q, p)
		if err != nil {
			return nil, fmt.Errorf("project %s/%s: %w", q.Group, p, err)
		}
		commits, err := c.directCommits(ctx, q, p, inMR)
		if err != nil {
			return nil, fmt.Errorf("project %s/%s: %w", q.Group, p, err)
		}
		items = append(items, mrs...)
		items = append(items, commits...)
	}
	return filterAndSort(items, include, q.ExcludeAuthors, q.ExcludeBots), nil
}

func (c *GitLab) get(ctx context.Context, path string, fn func([]byte) error) error {
	h := http.Header{}
	if c.Token != "" {
		h.Set("PRIVATE-TOKEN", c.Token)
	}
	return getPages(ctx, c.HTTP, c.BaseURL+"/api/v4"+path, h, fn)
}

// pathID is the URL-encoded full path GitLab accepts in place of a numeric
// project or group id. PathEscape encodes "/" as %2F.
func pathID(group, rel string) string {
	if rel == "" {
		return url.PathEscape(group)
	}
	return url.PathEscape(group + "/" + rel)
}

// projects merges the explicit list with projects found by topic, as paths
// relative to the group.
func (c *GitLab) projects(ctx context.Context, q GitLabQuery) ([]string, error) {
	projects := slices.Clone(q.Projects)
	for _, topic := range q.Topics {
		params := url.Values{"topic": {topic}, "include_subgroups": {"true"}, "archived": {"false"}, "with_shared": {"false"}, "per_page": {"100"}}
		err := c.get(ctx, "/groups/"+pathID(q.Group, "")+"/projects?"+params.Encode(), func(body []byte) error {
			var res []struct {
				PathWithNamespace string `json:"path_with_namespace"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return err
			}
			prefix := q.Group + "/"
			for _, r := range res {
				// Projects outside the group cannot be addressed relative to it.
				if len(r.PathWithNamespace) > len(prefix) && strings.EqualFold(r.PathWithNamespace[:len(prefix)], prefix) {
					projects = append(projects, r.PathWithNamespace[len(prefix):])
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("topic %q in %s: %w", topic, q.Group, err)
		}
	}
	slices.Sort(projects)
	return slices.Compact(projects), nil
}

// mergedMRs returns the MRs merged in the window and the set of commit SHAs
// that belong to them: merge and squash commits plus the MRs' own commits.
func (c *GitLab) mergedMRs(ctx context.Context, q GitLabQuery, project string) ([]Item, map[string]bool, error) {
	id := pathID(q.Group, project)
	params := url.Values{"state": {"merged"}, "updated_after": {q.From.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	var items []Item
	inMR := map[string]bool{}
	err := c.get(ctx, "/projects/"+id+"/merge_requests?"+params.Encode(), func(body []byte) error {
		var res []struct {
			IID         int    `json:"iid"`
			Title       string `json:"title"`
			Description string `json:"description"`
			WebURL      string `json:"web_url"`
			Author      struct {
				Username string `json:"username"`
			} `json:"author"`
			Labels          []string   `json:"labels"`
			MergedAt        *time.Time `json:"merged_at"`
			MergeCommitSHA  string     `json:"merge_commit_sha"`
			SquashCommitSHA string     `json:"squash_commit_sha"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return err
		}
		for _, mr := range res {
			// GitLab cannot filter on merge time, so updated_after over-fetches.
			if mr.MergedAt == nil || mr.MergedAt.Before(q.From) || !mr.MergedAt.Before(q.To) {
				continue
			}
			for _, sha := range []string{mr.MergeCommitSHA, mr.SquashCommitSHA} {
				if sha != "" {
					inMR[sha] = true
				}
			}
			items = append(items, Item{
				Repo: project, Kind: KindMR, Number: mr.IID, Title: mr.Title,
				Body: strings.TrimSpace(mr.Description), URL: mr.WebURL,
				Author: mr.Author.Username, Labels: mr.Labels, At: *mr.MergedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	for _, it := range items {
		path := fmt.Sprintf("/projects/%s/merge_requests/%d/commits?per_page=100", id, it.Number)
		err := c.get(ctx, path, func(body []byte) error {
			var res []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return err
			}
			for _, r := range res {
				inMR[r.ID] = true
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return items, inMR, nil
}

// directCommits lists default-branch commits in the window that are not merge
// commits and do not belong to a collected MR. GitLab gives no username for a
// commit, so Author is the commit email.
func (c *GitLab) directCommits(ctx context.Context, q GitLabQuery, project string, inMR map[string]bool) ([]Item, error) {
	params := url.Values{
		"since": {q.From.UTC().Format(time.RFC3339)},
		"until": {q.To.UTC().Format(time.RFC3339)},
		// Only the default branch's own line: feature commits of MRs merged
		// after the window are not direct commits.
		"first_parent": {"true"},
		"per_page":     {"100"},
	}
	var items []Item
	err := c.get(ctx, "/projects/"+pathID(q.Group, project)+"/repository/commits?"+params.Encode(), func(body []byte) error {
		var res []struct {
			ID           string    `json:"id"`
			Message      string    `json:"message"`
			WebURL       string    `json:"web_url"`
			AuthorEmail  string    `json:"author_email"`
			AuthoredDate time.Time `json:"authored_date"`
			ParentIDs    []string  `json:"parent_ids"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return err
		}
		for _, cm := range res {
			if len(cm.ParentIDs) > 1 || inMR[cm.ID] {
				continue
			}
			title, rest, _ := strings.Cut(cm.Message, "\n")
			items = append(items, Item{
				Repo: project, Kind: KindCommit, Title: strings.TrimSpace(title),
				Body: strings.TrimSpace(rest), URL: cm.WebURL, Author: cm.AuthorEmail, At: cm.AuthoredDate,
			})
		}
		return nil
	})
	return items, err
}

// includedAuthors returns the lowercased usernames and emails to keep, or nil
// when no include filter is set. A group counts its direct members and those
// of its subgroups, like a GitHub team with child teams; ancestor-group
// members are not counted.
func (c *GitLab) includedAuthors(ctx context.Context, q GitLabQuery) (map[string]bool, error) {
	if len(q.IncludeGroups) == 0 && len(q.IncludeAuthors) == 0 {
		return nil, nil
	}
	include := map[string]bool{}
	for _, a := range q.IncludeAuthors {
		include[strings.ToLower(a)] = true
	}
	for _, g := range q.IncludeGroups {
		full := q.Group + "/" + g
		groups := []string{full}
		err := c.get(ctx, "/groups/"+url.PathEscape(full)+"/descendant_groups?per_page=100", func(body []byte) error {
			var res []struct {
				FullPath string `json:"full_path"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				return err
			}
			for _, d := range res {
				groups = append(groups, d.FullPath)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("group %s subgroups: %w", full, err)
		}
		for _, path := range groups {
			err := c.get(ctx, "/groups/"+url.PathEscape(path)+"/members?per_page=100", func(body []byte) error {
				var res []struct {
					Username string `json:"username"`
				}
				if err := json.Unmarshal(body, &res); err != nil {
					return err
				}
				for _, m := range res {
					include[strings.ToLower(m.Username)] = true
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("group %s members: %w", path, err)
			}
		}
	}
	return include, nil
}
