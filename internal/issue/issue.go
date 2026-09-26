// Package issue models one weekly issue: its ISO week, its folder under
// issues/, and the front matter at the top of index.md.
package issue

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// ErrExists is returned by WriteNew when the file is already there.
var ErrExists = errors.New("file already exists")

var weekPattern = regexp.MustCompile(`^(\d{4})-W(\d{2})$`)

// Week is an ISO 8601 week, written as 2026-W39.
type Week struct {
	Year, Num int
}

func ParseWeek(s string) (Week, error) {
	m := weekPattern.FindStringSubmatch(s)
	if m == nil {
		return Week{}, fmt.Errorf("week %q: want the form 2026-W39", s)
	}
	year, _ := strconv.Atoi(m[1]) // the pattern guarantees digits
	num, _ := strconv.Atoi(m[2])
	w := Week{Year: year, Num: num}
	if w.Num < 1 || WeekOf(w.Start()) != w {
		return Week{}, fmt.Errorf("week %q does not exist", s)
	}
	return w, nil
}

// WeekOf returns the ISO week that contains t.
func WeekOf(t time.Time) Week {
	y, n := t.ISOWeek()
	return Week{Year: y, Num: n}
}

func (w Week) String() string {
	return fmt.Sprintf("%04d-W%02d", w.Year, w.Num)
}

// Start is Monday 00:00 UTC of the week. Week 1 is the week holding 4 January.
func (w Week) Start() time.Time {
	jan4 := time.Date(w.Year, time.January, 4, 0, 0, 0, 0, time.UTC)
	offset := (int(jan4.Weekday()) + 6) % 7 // days since Monday
	return jan4.AddDate(0, 0, -offset+(w.Num-1)*7)
}

// Window returns [from, to): the n weeks ending with w.
func (w Week) Window(n int) (from, to time.Time) {
	to = w.Start().AddDate(0, 0, 7)
	from = to.AddDate(0, 0, -7*n)
	return from, to
}

func Dir(root string, w Week) string {
	return filepath.Join(root, w.String())
}

// Latest returns the newest week folder under root.
func Latest(root string) (Week, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Week{}, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && weekPattern.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return Week{}, fmt.Errorf("no week folders under %s", root)
	}
	sort.Strings(names)
	return ParseWeek(names[len(names)-1])
}

// WriteNew writes content to path, creating parent folders. It refuses to
// replace an existing file unless force is set.
func WriteNew(path, content string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s: %w (use --force to replace it)", path, ErrExists)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

type FrontMatter struct {
	Title  string `yaml:"title"`
	Week   string `yaml:"week"`
	Period string `yaml:"period"`
}

func (fm FrontMatter) Render() string {
	b, _ := yaml.Marshal(fm)
	return "---\n" + string(b) + "---\n\n"
}

// SplitFrontMatter separates a leading YAML block from the markdown body.
// Content without front matter is returned unchanged as the body.
func SplitFrontMatter(content string) (FrontMatter, string, error) {
	var fm FrontMatter
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return fm, content, nil
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return fm, content, errors.New("front matter is not closed with ---")
	}
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		return fm, content, fmt.Errorf("front matter: %w", err)
	}
	return fm, strings.TrimLeft(body, "\n"), nil
}

// ReadFooter returns the hand-maintained footer at path, or "" when the file
// does not exist.
func ReadFooter(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// WithFooter appends footer to body below a horizontal rule. A blank footer
// leaves body unchanged.
func WithFooter(body, footer string) string {
	footer = strings.TrimSpace(footer)
	if footer == "" {
		return body
	}
	return strings.TrimRight(body, "\n") + "\n\n---\n\n" + footer + "\n"
}

// WithStats puts line on its own paragraph under the executive summary: the
// first paragraph after the ## headline, or the first paragraph when there is
// no headline. An empty line leaves body unchanged.
func WithStats(body, line string) string {
	if line == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	i := 0
	for j, l := range lines {
		if strings.HasPrefix(l, "## ") {
			i = j + 1
			break
		}
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
		i++
	}
	out := append([]string{}, lines[:i]...)
	out = append(out, "", line)
	return strings.Join(append(out, lines[i:]...), "\n")
}
