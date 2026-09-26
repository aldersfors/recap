package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func renderFixture(t *testing.T) (*Output, string) {
	t.Helper()
	dir, _ := filepath.Abs("testdata/2026-W39")
	content, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(dir, string(content))
	if err != nil {
		t.Fatal(err)
	}
	return out, dir
}

func TestMattermostGolden(t *testing.T) {
	out, _ := renderFixture(t)
	golden(t, "mattermost.golden.md", out.Mattermost)
}

func TestEmailGolden(t *testing.T) {
	out, dir := renderFixture(t)
	golden(t, "email.golden.html", strings.ReplaceAll(out.Email, dir, "ROOT"))
}

func TestMediaListAndWarnings(t *testing.T) {
	out, dir := renderFixture(t)
	var got []string
	for _, m := range out.Media {
		got = append(got, strings.TrimPrefix(m, dir+"/"))
	}
	if strings.Join(got, ",") != "media/login.png,media/tour.mp4,media/alerts.mov" {
		t.Errorf("Media = %v", got)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "MEDIA") {
		t.Errorf("Warnings = %v", out.Warnings)
	}
}

func TestWarnsOnTodoAndMissingMedia(t *testing.T) {
	out, err := Render(t.TempDir(), "TODO: summary\n\n![x](media/nope.png)\n")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out.Warnings, "\n")
	if !strings.Contains(joined, "TODO") || !strings.Contains(joined, "media/nope.png") {
		t.Errorf("Warnings = %v", out.Warnings)
	}
}

// shaped is a minimal issue body with every section, so a test sees only
// the warning it is about.
const shaped = "## Headline\n\nSummary.\n\n### Milestones\n\n- **A**: done.\n\n### Toil\n\n- **B**: fixed.\n\n### Demo\n\n- **C**: shown.\n"

func TestWarnsOnLongLines(t *testing.T) {
	long := "- **Long**: " + strings.Repeat("word ", 14) + "end." // 12 + 70 + 4 = 86
	content := "---\ntitle: T\n---\n\n" + shaped + long + "\n" +
		"<!-- MEDIA: " + strings.Repeat("x", 90) + " -->\n" +
		"  https://example.com/" + strings.Repeat("a", 80) + "\n"
	out, err := Render(t.TempDir(), content)
	if err != nil {
		t.Fatal(err)
	}
	var lineWarnings []string
	for _, w := range out.Warnings {
		if strings.Contains(w, "characters") {
			lineWarnings = append(lineWarnings, w)
		}
	}
	// Line 20 of the file is the long bullet. The MEDIA comment is dropped from
	// the output and a lone URL cannot be wrapped, so neither is flagged.
	if len(lineWarnings) != 1 || !strings.Contains(lineWarnings[0], "line 20 is 86 characters") {
		t.Errorf("line warnings = %v, want one for line 20", lineWarnings)
	}
}

func TestWarnsOnMissingSections(t *testing.T) {
	out, err := Render(t.TempDir(), "## Headline\n\nSummary.\n\n### Milestones\n\n- **A**: done.\n")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out.Warnings, "\n")
	for _, want := range []string{"### Toil", "### Demo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Warnings = %v, want one naming %s", out.Warnings, want)
		}
	}
	if strings.Contains(joined, "### Milestones") {
		t.Errorf("Milestones is present but flagged: %v", out.Warnings)
	}
}

func TestShapedIssueHasNoWarnings(t *testing.T) {
	out, err := Render(t.TempDir(), shaped)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", out.Warnings)
	}
}

func TestMattermostTitleOutranksSections(t *testing.T) {
	out, err := Render(t.TempDir(), "---\ntitle: Platform recap 2026-W39\n---\n\n"+shaped)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Mattermost, "# Platform recap 2026-W39\n") {
		t.Errorf("Mattermost starts %q, want a level-1 title above the headline and sections", out.Mattermost[:30])
	}
}

func TestEmailSectionsStaySmallerThanTitle(t *testing.T) {
	out, err := Render(t.TempDir(), "---\ntitle: T\n---\n\n"+shaped)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<h2 style="font-size:17px;margin:20px 0 8px">Headline</h2>`, `<h3 style="font-size:16px;margin:20px 0 8px">Milestones</h3>`} {
		if !strings.Contains(out.Email, want) {
			t.Errorf("email missing %s", want)
		}
	}
}
