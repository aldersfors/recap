package summarize

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jalet/recap/internal/activity"
	"github.com/jalet/recap/internal/issue"
)

type fakeProvider struct {
	system, user, reply string
}

func (f *fakeProvider) Complete(_ context.Context, system, user string) (string, error) {
	f.system, f.user = system, user
	return f.reply, nil
}

var brief = Brief{Team: "Platform", Audience: "the product teams", TicketRef: "acme/project#123"}

var fm = issue.FrontMatter{Title: "Platform recap 2026-W39", Week: "2026-W39", Period: "2026-09-21 to 2026-09-27"}

var items = []activity.Item{
	{Repo: "platform", Kind: activity.KindPR, Number: 12, Title: "Add Grafana SSO", Body: strings.Repeat("x", 5000), URL: "https://gh/12", At: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)},
	{Repo: "aws", Kind: activity.KindCommit, Title: "Rotate backup key", URL: "https://gh/c/b2"},
}

func TestDraftSendsPromptAndSanitizes(t *testing.T) {
	p := &fakeProvider{reply: "```markdown\nA calm week \u2014 SSO landed.\n\n- **Access**: Grafana uses SSO.\n```\n"}
	got, err := Draft(context.Background(), p, brief, fm, items)
	if err != nil {
		t.Fatal(err)
	}
	if want := "A calm week - SSO landed.\n\n- **Access**: Grafana uses SSO.\n"; got != want {
		t.Errorf("Draft = %q, want %q", got, want)
	}
	if !strings.Contains(p.system, "### Milestones") {
		t.Error("system prompt not sent")
	}
	for _, want := range []string{"2026-W39", "Add Grafana SSO", "Rotate backup key", "[truncated]"} {
		if !strings.Contains(p.user, want) {
			t.Errorf("user message missing %q", want)
		}
	}
	if strings.Contains(p.user, strings.Repeat("x", maxBody+1)) {
		t.Error("body not truncated")
	}
}

func TestDraftRejectsEmptyReply(t *testing.T) {
	if _, err := Draft(context.Background(), &fakeProvider{reply: "  \n"}, brief, fm, items); err == nil {
		t.Error("want error on empty reply")
	}
}

func TestSanitizeKeepsEnDashAndStripsFrontMatter(t *testing.T) {
	got := Sanitize("---\ntitle: x\n---\n\n2026\u20132027\u2014done")
	if got != "2026\u20132027-done\n" {
		t.Errorf("Sanitize = %q", got)
	}
}

func TestSkeletonListsActivity(t *testing.T) {
	got := Skeleton(items)
	for _, want := range []string{"TODO", "- platform: Add Grafana SSO ([#12](https://gh/12))", "- aws: Rotate backup key ([commit](https://gh/c/b2))", "<!-- MEDIA:"} {
		if !strings.Contains(got, want) {
			t.Errorf("skeleton missing %q:\n%s", want, got)
		}
	}
}

func TestSkeletonRefsMRsWithBang(t *testing.T) {
	got := Skeleton([]activity.Item{{Repo: "infra", Kind: activity.KindMR, Number: 5, Title: "Add SSO", URL: "https://gl/mr/5"}})
	if !strings.Contains(got, "- infra: Add SSO ([!5](https://gl/mr/5))") {
		t.Errorf("skeleton = %q, want an !5 reference", got)
	}
}

func TestDraftOmitsAuthors(t *testing.T) {
	p := &fakeProvider{reply: "ok"}
	in := []activity.Item{{Repo: "infra", Kind: activity.KindCommit, Title: "Rotate key", Author: "bob@corp.example", URL: "https://gl/c/d1"}}
	if _, err := Draft(context.Background(), p, brief, fm, in); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.user, "bob@corp.example") || strings.Contains(p.user, `"author"`) {
		t.Errorf("user message leaks the author: %s", p.user)
	}
	if in[0].Author != "bob@corp.example" {
		t.Error("Draft must not modify the caller's items")
	}
}

func TestDraftStripsIdentityTrailers(t *testing.T) {
	p := &fakeProvider{reply: "ok"}
	in := []activity.Item{{Repo: "infra", Kind: activity.KindCommit, Title: "Rotate key",
		Body: "Yearly rotation.\n\nSigned-off-by: Bob Smith <bob@corp.example>\nCo-authored-by: Al Jones <al@corp.example>", URL: "https://gl/c/d1"}}
	if _, err := Draft(context.Background(), p, brief, fm, in); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"Bob Smith", "bob@corp.example", "Al Jones", "al@corp.example"} {
		if strings.Contains(p.user, leak) {
			t.Errorf("user message leaks %q", leak)
		}
	}
	if !strings.Contains(p.user, "Yearly rotation.") {
		t.Error("the rest of the body must survive")
	}
}

func TestPromptAsksForTheIssueShape(t *testing.T) {
	for _, want := range []string{"## ", "### Milestones", "### Toil", "### Demo", "{{ticket}}", "80 characters"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestSkeletonHasTheIssueSections(t *testing.T) {
	got := Skeleton(items)
	var last int
	for _, h := range []string{"## ", "### Milestones", "### Toil", "### Demo", "<!-- MEDIA:", "<!-- Raw activity"} {
		i := strings.Index(got, h)
		if i < last {
			t.Fatalf("skeleton missing %q or out of order:\n%s", h, got)
		}
		last = i
	}
}

func TestDraftFillsInTheTeam(t *testing.T) {
	p := &fakeProvider{reply: "ok"}
	if _, err := Draft(context.Background(), p, brief, fm, items); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"the Platform team sends to the product teams", "`acme/project#123`"} {
		if !strings.Contains(p.system, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
	if strings.Contains(p.system, "{{") {
		t.Errorf("system prompt has an unfilled placeholder:\n%s", p.system)
	}
}

func TestTicketsFindsBothReferenceForms(t *testing.T) {
	got := brief.Tickets([]activity.Item{
		{Title: "Add widget (acme/project#11)"},
		{Title: "Fix widget", Body: "Part of https://github.com/acme/project/issues/12 and ACME/project#11."},
		{Title: "Unrelated", Body: "Fixes acme/other#5 and #7."},
	})
	if want := []int{11, 12}; !slices.Equal(got, want) {
		t.Errorf("Tickets = %v, want %v", got, want)
	}
}

func TestUserMessageGroupsByEpicLargestFirst(t *testing.T) {
	b := brief
	b.Epics = map[int]activity.Epic{
		11: {Ref: "acme/project#1", Title: "Parent epic"},
		12: {Ref: "acme/project#1", Title: "Parent epic"},
	}
	at := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	got := userMessage(b, fm, []activity.Item{
		{Repo: "web", Kind: activity.KindPR, Title: "Untracked change", At: at},
		{Repo: "web", Kind: activity.KindPR, Title: "Ticket change", Body: "acme/project#30", At: at},
		{Repo: "web", Kind: activity.KindPR, Title: "Epic change one", Body: "acme/project#11", At: at},
		{Repo: "web", Kind: activity.KindPR, Title: "Epic change two", Body: "See acme/project#99 and acme/project#12.", At: at},
	})
	epic := strings.Index(got, "## Epic acme/project#1: Parent epic (2 items)")
	ticket := strings.Index(got, "## Ticket acme/project#30 (1 items)")
	loose := strings.Index(got, "## Other, no ticket (1 items)")
	if epic < 0 || ticket < 0 || loose < 0 {
		t.Fatalf("missing a group header:\n%s", got)
	}
	if epic > ticket || ticket > loose {
		t.Errorf("groups out of order (epic %d, ticket %d, loose %d):\n%s", epic, ticket, loose, got)
	}
	// The second item refers first to an unresolved ticket, then to one under
	// the epic: the epic wins.
	if i := strings.Index(got, "Epic change two"); i < epic || i > ticket {
		t.Errorf("item with a resolvable ticket was not grouped under its epic:\n%s", got)
	}
}

func TestUserMessageGroupsUntrackedWorkByScope(t *testing.T) {
	got := userMessage(brief, fm, []activity.Item{
		{Repo: "web", Kind: activity.KindPR, Title: "feat(api): add endpoint"},
		{Repo: "web", Kind: activity.KindPR, Title: "Fix(API): handle errors"},
		{Repo: "web", Kind: activity.KindPR, Title: "fix(ui): one-off"},
		{Repo: "web", Kind: activity.KindPR, Title: "Bump deps"},
	})
	scope := strings.Index(got, "## Scope api, no ticket (2 items)")
	other := strings.Index(got, "## Other, no ticket (2 items)")
	if scope < 0 || other < scope {
		t.Fatalf("want an api scope group before the rest:\n%s", got)
	}
	// A scope used once is not a theme: it joins the rest.
	if i := strings.Index(got, "fix(ui): one-off"); i < other {
		t.Errorf("single-item scope got its own group:\n%s", got)
	}
}
