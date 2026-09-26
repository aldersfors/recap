// Package summarize turns a week's activity into a draft update, either with
// Claude or as a hand-editing skeleton.
package summarize

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jalet/recap/internal/activity"
	"github.com/jalet/recap/internal/issue"
)

//go:embed prompt.md
var systemPrompt string

// maxBody caps each PR or commit body sent to the model. PR templates and
// long changelogs add tokens without adding signal for a summary.
const maxBody = 1500

// identityTrailer matches git trailers that name a person, such as
// Signed-off-by and Co-authored-by, with the name and email they carry.
var identityTrailer = regexp.MustCompile(`(?im)^[a-z][a-z-]*-by:.*$\n?`)

// Provider sends one system + user prompt to a model and returns its text.
type Provider interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// Brief says who the update is from and for, and what a ticket reference
// looks like, for the prompt.
type Brief struct {
	Team      string // e.g. "Operations"
	Audience  string // e.g. "the whole organization"
	TicketRef string // e.g. "acme/project#123"
}

func (b Brief) system() string {
	ticket := b.TicketRef
	if ticket == "" {
		ticket = "#123"
	}
	return strings.NewReplacer("{{team}}", b.Team, "{{audience}}", b.Audience, "{{ticket}}", ticket).Replace(systemPrompt)
}

// Draft asks the provider for the update body and returns it sanitized.
func Draft(ctx context.Context, p Provider, b Brief, fm issue.FrontMatter, items []activity.Item) (string, error) {
	user, err := userMessage(fm, items)
	if err != nil {
		return "", err
	}
	out, err := p.Complete(ctx, b.system(), user)
	if err != nil {
		return "", err
	}
	out = Sanitize(out)
	if strings.TrimSpace(out) == "" {
		return "", errors.New("model returned an empty draft")
	}
	return out, nil
}

func userMessage(fm issue.FrontMatter, items []activity.Item) (string, error) {
	trimmed := make([]activity.Item, len(items))
	for i, it := range items {
		it.Body = strings.TrimSpace(identityTrailer.ReplaceAllString(it.Body, ""))
		if r := []rune(it.Body); len(r) > maxBody {
			it.Body = string(r[:maxBody]) + " [truncated]"
		}
		// The prompt never names people, so authors (logins, and emails on
		// GitLab) and identity trailers stay out of what the model receives.
		it.Author = ""
		trimmed[i] = it
	}
	data, err := json.MarshalIndent(trimmed, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Week: %s (%s)\nActivity items: %d\n\n<activity>\n%s\n</activity>\n",
		fm.Week, fm.Period, len(items), data), nil
}

// Sanitize strips wrappers models sometimes add (code fences, front matter)
// and replaces em-dashes, which the org style bans.
func Sanitize(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if _, rest, ok := strings.Cut(s, "\n"); ok {
			s = strings.TrimSuffix(strings.TrimSpace(rest), "```")
		}
	}
	if _, body, err := issue.SplitFrontMatter(s); err == nil {
		s = body
	}
	s = strings.ReplaceAll(s, " \u2014 ", " - ")
	s = strings.ReplaceAll(s, "\u2014", "-")
	return strings.TrimSpace(s) + "\n"
}

// Skeleton is the --no-ai draft: the fixed shape plus the raw activity to
// rewrite by hand.
func Skeleton(items []activity.Item) string {
	var b strings.Builder
	b.WriteString("## TODO headline, at most 10 words\n\n")
	b.WriteString("TODO: one or two sentences on what the week delivered and why it matters.\n\n")
	b.WriteString("### Milestones\n\n- **TODO delivery**: finished epic work, one or two sentences.\n\n")
	b.WriteString("### Toil\n\n- **TODO task**: smaller upkeep, one sentence.\n\n")
	b.WriteString("### Demo\n\n- **TODO demo**: the week's biggest result.\n")
	b.WriteString("<!-- MEDIA: add ![caption](media/file.png) or media/file.mp4 for the demo -->\n\n")
	b.WriteString("<!-- Raw activity below: turn it into the bullets above, then delete this list. -->\n\n")
	if len(items) == 0 {
		b.WriteString("- No merged PRs or direct commits in this window.\n")
	}
	for _, it := range items {
		ref := "commit"
		switch it.Kind {
		case activity.KindPR:
			ref = fmt.Sprintf("#%d", it.Number)
		case activity.KindMR:
			ref = fmt.Sprintf("!%d", it.Number)
		}
		fmt.Fprintf(&b, "- %s: %s ([%s](%s))\n", it.Repo, it.Title, ref, it.URL)
	}
	return b.String()
}
