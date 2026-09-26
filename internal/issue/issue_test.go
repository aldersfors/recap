package issue

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseWeekRoundTrip(t *testing.T) {
	for _, s := range []string{"2026-W39", "2026-W53", "2027-W01"} {
		w, err := ParseWeek(s)
		if err != nil {
			t.Fatalf("ParseWeek(%q): %v", s, err)
		}
		if w.String() != s {
			t.Errorf("String() = %q, want %q", w.String(), s)
		}
	}
}

func TestParseWeekRejects(t *testing.T) {
	for _, s := range []string{"2026-39", "2026-W00", "2026-W54", "2025-W53", "W39"} {
		if _, err := ParseWeek(s); err == nil {
			t.Errorf("ParseWeek(%q) succeeded, want error", s)
		}
	}
}

func TestStartAndWeekOfAcrossYearBoundary(t *testing.T) {
	tests := []struct {
		week  string
		start string
	}{
		{"2026-W39", "2026-09-21"},
		{"2026-W53", "2026-12-28"},
		{"2027-W01", "2027-01-04"},
		{"2026-W01", "2025-12-29"},
	}
	for _, tt := range tests {
		w, _ := ParseWeek(tt.week)
		if got := w.Start(); !got.Equal(date(tt.start)) {
			t.Errorf("%s Start() = %s, want %s", tt.week, got.Format("2006-01-02"), tt.start)
		}
		if got := WeekOf(date(tt.start).Add(50 * time.Hour)); got != w {
			t.Errorf("WeekOf(%s+50h) = %s, want %s", tt.start, got, w)
		}
	}
	if got := WeekOf(date("2027-01-02")); got.String() != "2026-W53" {
		t.Errorf("WeekOf(2027-01-02) = %s, want 2026-W53", got)
	}
}

func TestWindow(t *testing.T) {
	w, _ := ParseWeek("2027-W01")
	from, to := w.Window(2)
	if !from.Equal(date("2026-12-28")) || !to.Equal(date("2027-01-11")) {
		t.Errorf("Window(2) = %s..%s", from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
}

func TestWriteNewRefusesToClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2026-W39", "index.md")
	if err := WriteNew(path, "one", false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteNew(path, "two", false); !errors.Is(err, ErrExists) {
		t.Fatalf("second write err = %v, want ErrExists", err)
	}
	if err := WriteNew(path, "three", true); err != nil {
		t.Fatalf("forced write: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "three" {
		t.Errorf("content = %q", b)
	}
}

func TestLatest(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"2026-W09", "2026-W39", "2026-W10", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := Latest(root)
	if err != nil || w.String() != "2026-W39" {
		t.Fatalf("Latest = %v, %v", w, err)
	}
	if _, err := Latest(t.TempDir()); err == nil {
		t.Error("Latest on empty dir succeeded")
	}
}

func TestSplitFrontMatter(t *testing.T) {
	fm, body, err := SplitFrontMatter("---\ntitle: Platform recap\nweek: 2026-W39\nperiod: 2026-09-21 to 2026-09-27\n---\n\nHello\n")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Title != "Platform recap" || fm.Week != "2026-W39" || body != "Hello\n" {
		t.Errorf("fm = %+v body = %q", fm, body)
	}
	if _, body, _ := SplitFrontMatter("no front matter"); body != "no front matter" {
		t.Errorf("body = %q", body)
	}
}

func TestWithFooterAppendsAfterRule(t *testing.T) {
	got := WithFooter("### Headline\n\nBody.\n", "\n- [Grafana](https://grafana.example)\n\n")
	want := "### Headline\n\nBody.\n\n---\n\n- [Grafana](https://grafana.example)\n"
	if got != want {
		t.Errorf("WithFooter = %q, want %q", got, want)
	}
}

func TestWithFooterSkipsEmptyFooter(t *testing.T) {
	if got := WithFooter("Body.\n", " \n"); got != "Body.\n" {
		t.Errorf("WithFooter with blank footer = %q, want the body unchanged", got)
	}
}

func TestReadFooterMissingFileIsEmpty(t *testing.T) {
	got, err := ReadFooter(filepath.Join(t.TempDir(), "footer.md"))
	if err != nil || got != "" {
		t.Errorf("ReadFooter(missing) = %q, %v; want empty and no error", got, err)
	}
}

func TestReadFooterReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "footer.md")
	if err := os.WriteFile(path, []byte("- [Plane](https://plane.example)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFooter(path)
	if err != nil || got != "- [Plane](https://plane.example)\n" {
		t.Errorf("ReadFooter = %q, %v", got, err)
	}
}

func TestWithStatsGoesUnderTheSummary(t *testing.T) {
	body := "## Headline\n\nSummary line one\nand two.\n\n### Milestones\n\n- **A**: done.\n"
	want := "## Headline\n\nSummary line one\nand two.\n\n*Closed: 3.*\n\n### Milestones\n\n- **A**: done.\n"
	if got := WithStats(body, "*Closed: 3.*"); got != want {
		t.Errorf("WithStats =\n%s\nwant\n%s", got, want)
	}
}

func TestWithStatsWithoutHeadline(t *testing.T) {
	got := WithStats("Summary.\n\n### Milestones\n", "*Closed: 3.*")
	if want := "Summary.\n\n*Closed: 3.*\n\n### Milestones\n"; got != want {
		t.Errorf("WithStats = %q, want %q", got, want)
	}
}

func TestWithStatsEmptyLineLeavesBody(t *testing.T) {
	if got := WithStats("## H\n\nS.\n", ""); got != "## H\n\nS.\n" {
		t.Errorf("WithStats with no line = %q", got)
	}
}
