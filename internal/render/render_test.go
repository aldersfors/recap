package render

import (
	"bytes"
	"encoding/base64"
	"flag"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
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

// emlPart is one leaf of the parsed .eml.
type emlPart struct {
	contentType, disposition, cid, filename string
	body                                    []byte
}

// readParts walks a multipart body, descending into nested multiparts. The
// standard library decodes quoted-printable itself; base64 is decoded here.
func readParts(t *testing.T, r io.Reader, boundary string) []emlPart {
	t.Helper()
	var parts []emlPart
	mr := multipart.NewReader(r, boundary)
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Fatal(err)
		}
		mt, params, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(mt, "multipart/") {
			parts = append(parts, readParts(t, p, params["boundary"])...)
			continue
		}
		body, err := io.ReadAll(p)
		if err != nil {
			t.Fatal(err)
		}
		if p.Header.Get("Content-Transfer-Encoding") == "base64" {
			if body, err = io.ReadAll(base64Decoder(body)); err != nil {
				t.Fatal(err)
			}
		}
		disp, dparams, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		parts = append(parts, emlPart{
			contentType: mt, disposition: disp, filename: dparams["filename"],
			cid: strings.Trim(p.Header.Get("Content-Id"), "<>"), body: body,
		})
	}
}

func TestEMLDraft(t *testing.T) {
	out, dir := renderFixture(t)
	msg, err := mail.ReadMessage(bytes.NewReader(out.EML))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); got != "Platform recap 2026-W39" {
		t.Errorf("Subject = %q", got)
	}
	if msg.Header.Get("X-Unsent") != "1" {
		t.Error("X-Unsent is not set, so Outlook would not open the file as a draft")
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("Content-Type = %q, %v", mt, err)
	}
	parts := readParts(t, msg.Body, params["boundary"])

	var html string
	inline := map[string]emlPart{}
	var attached []string
	for _, p := range parts {
		switch {
		case p.contentType == "text/html":
			html = string(p.body)
		case p.disposition == "inline":
			inline[p.cid] = p
		case p.disposition == "attachment":
			attached = append(attached, p.filename)
			want, _ := os.ReadFile(filepath.Join(dir, "media", p.filename))
			if !bytes.Equal(p.body, want) {
				t.Errorf("%s body = %q, want %q", p.filename, p.body, want)
			}
		}
	}
	for cid, file := range map[string]string{"media-1@recap": "login.png", "media-2@recap": "tour.gif"} {
		if !strings.Contains(html, `src="cid:`+cid+`"`) {
			t.Errorf("html does not reference cid:%s", cid)
		}
		p, ok := inline[cid]
		want, _ := os.ReadFile(filepath.Join(dir, "media", file))
		if !ok || p.filename != file || !bytes.Equal(p.body, want) {
			t.Errorf("inline %s = %+v, want %s", cid, p, file)
		}
	}
	if len(inline) != 2 {
		t.Errorf("inline parts = %d, want 2", len(inline))
	}
	if strings.Join(attached, ",") != "tour.mp4,alerts.mov" {
		t.Errorf("attachments = %v", attached)
	}
	if !strings.Contains(html, `src="https://status.example.com/badge.png"`) {
		t.Error("remote image was not left as a link")
	}
	if strings.Contains(html, "file://") || strings.Contains(html, "data:") {
		t.Error("the .eml html points at local files or data: URLs instead of cid:")
	}
}

func base64Decoder(b []byte) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, bytes.NewReader(b))
}
