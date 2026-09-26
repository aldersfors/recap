package summarize

import (
	"context"
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
