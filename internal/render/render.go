// Package render turns a finished index.md into paste-ready output: markdown
// for Mattermost and an HTML page to copy into Outlook.
package render

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"

	"github.com/jalet/recap/internal/issue"
)

type Output struct {
	Mattermost string
	Email      string
	Media      []string // absolute paths of local media, in document order
	Warnings   []string
}

var (
	imageRef  = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	comment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	blankRuns = regexp.MustCompile(`\n{3,}`)
	videoExts = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".m4v": true}
)

// Render reads media relative to dir, the issue's folder.
func Render(dir, content string) (*Output, error) {
	fm, body, err := issue.SplitFrontMatter(content)
	if err != nil {
		return nil, err
	}
	out := &Output{}
	if strings.Contains(body, "TODO") {
		out.Warnings = append(out.Warnings, "draft still contains TODO placeholders")
	}
	if n := strings.Count(body, "<!-- MEDIA:"); n > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d MEDIA placeholder(s) not replaced with media (they are dropped from the output)", n))
	}
	if strings.Contains(body, "\u2014") {
		out.Warnings = append(out.Warnings, "draft contains an em-dash")
	}
	out.Warnings = append(out.Warnings, longLines(content)...)
	for _, h := range sections {
		if !hasHeading(body, h) {
			out.Warnings = append(out.Warnings, fmt.Sprintf("draft has no %q heading", h))
		}
	}
	body = comment.ReplaceAllString(body, "")

	for _, m := range imageRef.FindAllStringSubmatch(body, -1) {
		if isRemote(m[2]) {
			continue
		}
		abs := filepath.Join(dir, m[2])
		if _, err := os.Stat(abs); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("media file not found: %s", m[2]))
		}
		out.Media = append(out.Media, abs)
	}

	out.Mattermost = mattermost(fm, body)
	out.Email, err = email(dir, fm, body)
	return out, err
}

// maxWidth is the column limit the prompt asks for: the email is read in a
// monospaced font.
const maxWidth = 80

// sections are the headings every issue carries, in the prompt's order.
var sections = []string{"### Milestones", "### Toil", "### Demo"}

// longLines flags lines wider than maxWidth, numbered as in the file. MEDIA
// comments are dropped from the output and a line holding a single word, such
// as a lone URL, cannot be wrapped, so neither is flagged.
func longLines(content string) []string {
	var warnings []string
	for i, line := range strings.Split(content, "\n") {
		n := utf8.RuneCountInString(line)
		if n <= maxWidth || strings.HasPrefix(strings.TrimSpace(line), "<!--") {
			continue
		}
		if len(strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "- "))) == 1 {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("line %d is %d characters, over the %d-column limit", i+1, n, maxWidth))
	}
	return warnings
}

// hasHeading reports whether body has heading h on a line of its own.
func hasHeading(body, h string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == h {
			return true
		}
	}
	return false
}

func isRemote(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}

// mattermost drops local media references; those files are attached to the
// post by hand. Remote images stay, Mattermost previews them inline.
func mattermost(fm issue.FrontMatter, body string) string {
	body = imageRef.ReplaceAllStringFunc(body, func(tok string) string {
		if isRemote(imageRef.FindStringSubmatch(tok)[2]) {
			return tok
		}
		return ""
	})
	var b strings.Builder
	if fm.Title != "" {
		// Level 1, so the title outranks the draft's ## headline and ### sections.
		fmt.Fprintf(&b, "# %s\n\n", fm.Title)
	}
	b.WriteString(tidy(body))
	return b.String()
}

// email points images at local files so a browser can show them and the
// copy into Outlook carries them along. Outlook cannot play video, so a video
// shows its sibling .gif when there is one, and a caption otherwise.
func email(dir string, fm issue.FrontMatter, body string) (string, error) {
	body = imageRef.ReplaceAllStringFunc(body, func(tok string) string {
		m := imageRef.FindStringSubmatch(tok)
		alt, ref := m[1], m[2]
		if isRemote(ref) {
			return tok
		}
		ext := strings.ToLower(filepath.Ext(ref))
		if !videoExts[ext] {
			return fmt.Sprintf("![%s](%s)", alt, fileURL(filepath.Join(dir, ref)))
		}
		caption := fmt.Sprintf("*Video: %s (attached as %s)*", alt, filepath.Base(ref))
		gif := strings.TrimSuffix(ref, filepath.Ext(ref)) + ".gif"
		if _, err := os.Stat(filepath.Join(dir, gif)); err == nil {
			return fmt.Sprintf("![%s](%s)\n\n%s", alt, fileURL(filepath.Join(dir, gif)), caption)
		}
		return caption
	})

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(gmhtml.WithUnsafe()), // allows file:// image URLs
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(tidy(body)), &buf); err != nil {
		return "", err
	}
	content := strings.NewReplacer(
		"<img ", `<img style="max-width:100%;height:auto;border-radius:6px" `,
		// The page title is a 20px h2; keep the draft's own headings below it.
		"<h2>", `<h2 style="font-size:17px;margin:20px 0 8px">`,
		"<h3>", `<h3 style="font-size:16px;margin:20px 0 8px">`,
	).Replace(buf.String())
	return fmt.Sprintf(emailPage, html.EscapeString(fm.Title), html.EscapeString(fm.Title), content), nil
}

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func tidy(s string) string {
	return strings.TrimSpace(blankRuns.ReplaceAllString(s, "\n\n")) + "\n"
}

const emailPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>%s</title>
</head>
<body style="margin:0;padding:24px;background:#ffffff">
<div style="max-width:640px;font-family:'Segoe UI',Arial,sans-serif;font-size:15px;line-height:1.5;color:#1f2328">
<h2 style="font-size:20px;margin:0 0 12px">%s</h2>
%s</div>
</body>
</html>
`
