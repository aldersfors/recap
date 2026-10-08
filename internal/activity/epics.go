package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Epic is the issue a ticket rolls up to: its nearest ancestor of type Epic,
// or the topmost ancestor when none is typed Epic.
type Epic struct {
	Ref   string // owner/repo#number
	Title string
}

// maxParentDepth bounds the walk up the sub-issue tree.
const maxParentDepth = 4

// Epics resolves each ticket in owner/repo to its epic. A ticket that cannot
// be fetched is left out, so a missing permission weakens the grouping of the
// draft instead of failing it.
func (c *Client) Epics(ctx context.Context, owner, repo string, numbers []int) map[int]Epic {
	cache := map[string]issueNode{}
	epics := map[int]Epic{}
	for _, n := range numbers {
		path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, n)
		node, err := c.issue(ctx, path, cache)
		if err != nil {
			continue
		}
		for depth := 0; node.Type != "Epic" && node.parentPath != "" && depth < maxParentDepth; depth++ {
			parent, err := c.issue(ctx, node.parentPath, cache)
			if err != nil {
				break
			}
			node = parent
		}
		epics[n] = Epic{Ref: node.ref, Title: node.Title}
	}
	return epics
}

type issueNode struct {
	ref, parentPath string
	Title, Type     string
}

func (c *Client) issue(ctx context.Context, path string, cache map[string]issueNode) (issueNode, error) {
	if node, ok := cache[path]; ok {
		return node, nil
	}
	var res struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Type   *struct {
			Name string `json:"name"`
		} `json:"type"`
		ParentIssueURL string `json:"parent_issue_url"`
	}
	err := c.paginate(ctx, path, func(body []byte) error { return json.Unmarshal(body, &res) })
	if err != nil {
		return issueNode{}, err
	}
	// path is /repos/owner/repo/issues/n.
	parts := strings.Split(strings.Trim(path, "/"), "/")
	node := issueNode{ref: fmt.Sprintf("%s/%s#%d", parts[1], parts[2], res.Number), Title: res.Title}
	if res.Type != nil {
		node.Type = res.Type.Name
	}
	if res.ParentIssueURL != "" {
		node.parentPath = c.apiPath(res.ParentIssueURL)
	}
	cache[path] = node
	return node, nil
}

// apiPath turns an absolute API URL from a response into a path for
// paginate, dropping any prefix BaseURL already carries (GitHub Enterprise
// serves the API under /api/v3). It returns "" for a URL it cannot read.
func (c *Client) apiPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return ""
	}
	p := strings.TrimPrefix(u.Path, strings.TrimSuffix(base.Path, "/"))
	if !strings.HasPrefix(p, "/repos/") || len(strings.Split(strings.Trim(p, "/"), "/")) != 5 {
		return ""
	}
	return p
}
