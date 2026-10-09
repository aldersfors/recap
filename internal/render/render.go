// Package render turns a finished index.md into paste-ready output: markdown
// for Mattermost, an HTML page to copy into Outlook, and the same email as a
// draft .eml.
package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"

	"github.com/aldersfors/recap/internal/issue"
)

type Output struct {
	Mattermost string
	// Email is an HTML page with local images embedded as data: URLs, so a
	// copy from the browser carries the image bytes into any mail client.
	Email string
	// EML is the same email as an unsent draft: images inline, videos attached.
	EML      []byte
	Media    []string // absolute paths of local media, in document order
	Warnings []string
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
	out.Email, err = email(dir, fm, body, dataURL)
	if err != nil {
		return nil, err
	}
	var inline []string
	page, err := email(dir, fm, body, func(path string) string {
		inline = append(inline, path)
		return "cid:" + contentID(len(inline))
	})
	if err != nil {
		return nil, err
	}
	var videos []string
	for _, m := range out.Media {
		if videoExts[strings.ToLower(filepath.Ext(m))] {
			videos = append(videos, m)
		}
	}
	out.EML, err = eml(fm.Title, page, inline, videos)
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

// email renders the HTML message. src turns a local media path into the image
// URL: a data: URL for the browser page, a cid: reference for the .eml. Mail
// clients cannot play video, so a video shows its sibling .gif when there is
// one, and a caption otherwise.
func email(dir string, fm issue.FrontMatter, body string, src func(path string) string) (string, error) {
	body = imageRef.ReplaceAllStringFunc(body, func(tok string) string {
		m := imageRef.FindStringSubmatch(tok)
		alt, ref := m[1], m[2]
		if isRemote(ref) {
			return tok
		}
		ext := strings.ToLower(filepath.Ext(ref))
		if !videoExts[ext] {
			return fmt.Sprintf("![%s](%s)", alt, src(filepath.Join(dir, ref)))
		}
		caption := fmt.Sprintf("*Video: %s (attached as %s)*", alt, filepath.Base(ref))
		gif := strings.TrimSuffix(ref, filepath.Ext(ref)) + ".gif"
		if _, err := os.Stat(filepath.Join(dir, gif)); err == nil {
			return fmt.Sprintf("![%s](%s)\n\n%s", alt, src(filepath.Join(dir, gif)), caption)
		}
		return caption
	})

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(gmhtml.WithUnsafe()), // allows data:, cid: and file:// image URLs
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

// mediaTypes is fixed rather than read from the system, so output is the same
// on every machine.
var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".mp4": "video/mp4", ".m4v": "video/mp4",
	".mov": "video/quicktime", ".webm": "video/webm",
}

func mediaType(path string) string {
	if t, ok := mediaTypes[strings.ToLower(filepath.Ext(path))]; ok {
		return t
	}
	return "application/octet-stream"
}

// dataURL embeds the file. A file that cannot be read keeps a file:// URL;
// Render has already warned that it is missing.
func dataURL(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fileURL(path)
	}
	return "data:" + mediaType(path) + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func contentID(n int) string {
	return fmt.Sprintf("media-%d@recap", n)
}

// eml builds an unsent draft: multipart/mixed holding a multipart/related
// (the HTML and its inline images, by Content-ID) and the videos as
// attachments. Boundaries are fixed so the output is reproducible. Files that
// cannot be read are left out; Render has already warned about them.
func eml(subject, page string, inline, attach []string) ([]byte, error) {
	var body bytes.Buffer
	mixed := multipart.NewWriter(&body)
	if err := mixed.SetBoundary("recap-mixed"); err != nil {
		return nil, err
	}
	var related bytes.Buffer
	rel := multipart.NewWriter(&related)
	if err := rel.SetBoundary("recap-related"); err != nil {
		return nil, err
	}

	w, err := rel.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/html; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, err
	}
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(page)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	for i, path := range inline {
		if err := filePart(rel, path, "inline", contentID(i+1)); err != nil {
			return nil, err
		}
	}
	if err := rel.Close(); err != nil {
		return nil, err
	}

	w, err = mixed.CreatePart(textproto.MIMEHeader{
		"Content-Type": {`multipart/related; boundary="recap-related"; type="text/html"`},
	})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(related.Bytes()); err != nil {
		return nil, err
	}
	for _, path := range attach {
		if err := filePart(mixed, path, "attachment", ""); err != nil {
			return nil, err
		}
	}
	if err := mixed.Close(); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	// X-Unsent makes Outlook open the file as a draft to address and send.
	fmt.Fprintf(&out, "MIME-Version: 1.0\r\nX-Unsent: 1\r\nSubject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	out.WriteString("Content-Type: multipart/mixed; boundary=\"recap-mixed\"\r\n\r\n")
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// filePart adds path as a base64 part, inline with a Content-ID or attached.
func filePart(mw *multipart.Writer, path, disposition, cid string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	name := filepath.Base(path)
	h := textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType(mediaType(path), map[string]string{"name": name})},
		"Content-Disposition":       {mime.FormatMediaType(disposition, map[string]string{"filename": name})},
		"Content-Transfer-Encoding": {"base64"},
	}
	if cid != "" {
		h.Set("Content-ID", "<"+cid+">")
	}
	w, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString(b)
	for len(enc) > 76 {
		if _, err := fmt.Fprintf(w, "%s\r\n", enc[:76]); err != nil {
			return err
		}
		enc = enc[76:]
	}
	_, err = fmt.Fprintf(w, "%s\r\n", enc)
	return err
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
