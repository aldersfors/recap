package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TicketStats counts issues closed as completed in a window.
type TicketStats struct {
	Tickets int
	Epics   int
}

// Line renders the stats as the italic line under the executive summary.
func (s TicketStats) Line(weeks int) string {
	period := "this week"
	if weeks > 1 {
		period = "this period"
	}
	return fmt.Sprintf("*Closed %s: %s, %s.*", period, plural(s.Tickets, "ticket"), plural(s.Epics, "epic"))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ClosedTickets counts issues in q.Org/repo closed as completed in the window
// that are assigned to a member of q's include filter. Unassigned issues and
// issues assigned only to others never count. Issues of type Epic count as
// epics, everything else as tickets.
func (c *Client) ClosedTickets(ctx context.Context, q Query, repo string) (TicketStats, error) {
	if len(q.IncludeTeams) == 0 && len(q.IncludeAuthors) == 0 {
		return TicketStats{}, errors.New("closed tickets need include_teams or include_authors to know whose work counts")
	}
	include, err := c.includedAuthors(ctx, q)
	if err != nil {
		return TicketStats{}, err
	}
	// closed: ranges are inclusive dates while To is exclusive, as for merged PRs.
	last := q.To.Add(-time.Nanosecond)
	closed := fmt.Sprintf("%s..%s", q.From.Format(time.DateOnly), last.Format(time.DateOnly))
	params := url.Values{
		"q":        {fmt.Sprintf("repo:%s/%s is:issue is:closed closed:%s", q.Org, repo, closed)},
		"per_page": {"100"},
	}
	var stats TicketStats
	err = c.paginate(ctx, "/search/issues?"+params.Encode(), func(body []byte) error {
		var res struct {
			Items []struct {
				StateReason string `json:"state_reason"`
				Type        *struct {
					Name string `json:"name"`
				} `json:"type"`
				Assignees []struct {
					Login string `json:"login"`
				} `json:"assignees"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return err
		}
		for _, it := range res.Items {
			if it.StateReason != "completed" {
				continue
			}
			assigned := false
			for _, a := range it.Assignees {
				assigned = assigned || include[strings.ToLower(a.Login)]
			}
			if !assigned {
				continue
			}
			if it.Type != nil && it.Type.Name == "Epic" {
				stats.Epics++
			} else {
				stats.Tickets++
			}
		}
		return nil
	})
	if err != nil {
		return TicketStats{}, fmt.Errorf("closed tickets in %s/%s: %w", q.Org, repo, err)
	}
	return stats, nil
}
