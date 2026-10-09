// Package summarize turns a week's activity into a draft update, either with
// Claude or as a hand-editing skeleton.
package summarize

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aldersfors/recap/internal/activity"
	"github.com/aldersfors/recap/internal/issue"
)

//go:embed prompt.md
var systemPrompt string

// maxBody caps each PR or commit body sent to the model. PR templates and
// long changelogs add tokens without adding signal for a summary, and the
// opening of a body carries its what and why.
const maxBody = 800

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
	// Epics maps a ticket number in the TicketRef repo to its epic. Items are
	// grouped by epic in the user message; unresolved tickets group by ticket.
	Epics map[int]activity.Epic
}

// ticketPattern matches references to tickets in the TicketRef repo, written
// as owner/repo#123 or as an issue URL. Nil when TicketRef names no repo.
func (b Brief) ticketPattern() *regexp.Regexp {
	repo, _, ok := strings.Cut(b.TicketRef, "#")
	if !ok || !strings.Contains(repo, "/") {
		return nil
	}
	return regexp.MustCompile(`(?i)` + regexp.QuoteMeta(repo) + `(?:#|/issues/)(\d+)`)
}

// refs lists the ticket numbers an item references, in order of appearance.
func (b Brief) refs(it activity.Item) []int {
	re := b.ticketPattern()
	if re == nil {
		return nil
	}
	var out []int
	for _, m := range re.FindAllStringSubmatch(it.Title+"\n"+it.Body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// Tickets lists every ticket the items reference, for resolving Epics.
func (b Brief) Tickets(items []activity.Item) []int {
	var out []int
	for _, it := range items {
		for _, n := range b.refs(it) {
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	slices.Sort(out)
	return out
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
	user := userMessage(b, fm, items)
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

// group is one theme of the week: the items under one epic or ticket, the
// untracked items sharing a commit scope, or the untracked rest.
type group struct {
	kind  string // groupEpic, groupTicket, groupScope or groupOther
	ref   string // ticket reference, or the scope
	title string // the epic's title, when known
	items []activity.Item
}

const (
	groupEpic   = "Epic"
	groupTicket = "Ticket"
	groupScope  = "Scope"
	groupOther  = "Other"
)

// scopePattern reads the scope of a Conventional Commits title, as in
// "feat(monitoring): keep logs 100 days".
var scopePattern = regexp.MustCompile(`^[a-z]+\(([^)]+)\)!?:`)

// groups buckets items by the epic of the first ticket they reference that
// resolves to one, else by that first ticket. Items without a ticket group by
// commit scope when at least two share it. Larger groups come first, so the
// week's main themes lead; the untracked rest comes last.
func groups(b Brief, items []activity.Item) []group {
	byKey := map[string]*group{}
	var order []*group
	add := func(kind, ref, title string, it activity.Item) {
		key := kind + " " + ref
		g := byKey[key]
		if g == nil {
			g = &group{kind: kind, ref: ref, title: title}
			byKey[key] = g
			order = append(order, g)
		}
		g.items = append(g.items, it)
	}
	scoped := map[string][]activity.Item{}
	var scopes []string
	var other []activity.Item
	for _, it := range items {
		if refs := b.refs(it); len(refs) > 0 {
			kind, ref, title := groupTicket, fmt.Sprintf("%s#%d", strings.SplitN(b.TicketRef, "#", 2)[0], refs[0]), ""
			for _, n := range refs {
				if e, ok := b.Epics[n]; ok {
					kind, ref, title = groupEpic, e.Ref, e.Title
					break
				}
			}
			add(kind, ref, title, it)
			continue
		}
		if m := scopePattern.FindStringSubmatch(strings.ToLower(it.Title)); m != nil {
			if _, ok := scoped[m[1]]; !ok {
				scopes = append(scopes, m[1])
			}
			scoped[m[1]] = append(scoped[m[1]], it)
			continue
		}
		other = append(other, it)
	}
	for _, sc := range scopes {
		if len(scoped[sc]) < 2 {
			other = append(other, scoped[sc]...)
			continue
		}
		for _, it := range scoped[sc] {
			add(groupScope, sc, "", it)
		}
	}
	slices.SortStableFunc(order, func(x, y *group) int { return cmp.Compare(len(y.items), len(x.items)) })
	out := make([]group, 0, len(order)+1)
	for _, g := range order {
		out = append(out, *g)
	}
	if len(other) > 0 {
		out = append(out, group{kind: groupOther, items: other})
	}
	return out
}

func userMessage(b Brief, fm issue.FrontMatter, items []activity.Item) string {
	var s strings.Builder
	fmt.Fprintf(&s, "Week: %s (%s)\nActivity items: %d\n\n<activity>\n", fm.Week, fm.Period, len(items))
	for _, g := range groups(b, items) {
		switch g.kind {
		case groupEpic:
			fmt.Fprintf(&s, "\n## Epic %s: %s (%d items)\n", g.ref, g.title, len(g.items))
		case groupTicket:
			fmt.Fprintf(&s, "\n## Ticket %s (%d items)\n", g.ref, len(g.items))
		case groupScope:
			fmt.Fprintf(&s, "\n## Scope %s, no ticket (%d items)\n", g.ref, len(g.items))
		default:
			fmt.Fprintf(&s, "\n## Other, no ticket (%d items)\n", len(g.items))
		}
		for _, it := range g.items {
			// The prompt never names people, so authors (logins, and emails on
			// GitLab) and identity trailers stay out of what the model receives.
			body := strings.TrimSpace(identityTrailer.ReplaceAllString(it.Body, ""))
			if r := []rune(body); len(r) > maxBody {
				body = string(r[:maxBody]) + " [truncated]"
			}
			fmt.Fprintf(&s, "\n- [%s] %s: %s (%s)\n", it.Kind, it.Repo, it.Title, it.At.Format("2006-01-02"))
			if body != "" {
				s.WriteString("  " + strings.ReplaceAll(body, "\n", "\n  ") + "\n")
			}
		}
	}
	s.WriteString("</activity>\n")
	return s.String()
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
