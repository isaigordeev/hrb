// Package article writes Frontmatter+body markdown files for fetched
// items.
package article

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Kunde21/markdownfmt/v3"
	"gopkg.in/yaml.v3"

	"github.com/isdg/hr/internal/textfmt"
)

type Article struct {
	Title     string
	URL       string
	Published time.Time
	FeedName  string
	GUID      string
	Body      string
}

func (a *Article) Filename() string {
	return NameFor(a.Title, a.Published, a.ID())
}

// NameFor builds the canonical article filename "<date>-<slug>-<id>.md".
func NameFor(title string, published time.Time, id string) string {
	return fmt.Sprintf("%s-%s-%s.md",
		published.Format("2006-01-02"), slugify(title), id)
}

var idInName = regexp.MustCompile(`-([0-9a-f]{8})\.md$`)

// IDFromName extracts the stable 8-char id from a canonical filename.
func IDFromName(name string) (string, bool) {
	m := idInName.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ParseTime parses an RFC3339 timestamp, tolerating the signed/negative
// (BC) years that Go's RFC3339 layout rejects. Returns the zero time if
// unparseable.
func ParseTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	rest, ok := strings.CutPrefix(s, "-")
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, rest)
	if err != nil {
		return time.Time{}
	}
	return time.Date(-t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// ID returns a stable 8-char fingerprint for the article, derived from
// the GUID (or URL, or title+timestamp) so the same item produces the
// same filename across syncs.
func (a *Article) ID() string {
	src := a.GUID
	if src == "" {
		src = a.URL
	}
	if src == "" {
		src = a.Title + a.Published.Format(time.RFC3339)
	}
	h := sha1.Sum([]byte(src))
	return hex.EncodeToString(h[:])[:8]
}

var (
	nonAlphanum = regexp.MustCompile(`[^a-z0-9]+`)
	multiDash   = regexp.MustCompile(`-+`)
)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphanum.ReplaceAllString(s, "-")
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

// Write writes the article into feedDir if it doesn't already exist,
// creating the directory (and any parent groups) as needed. Callers
// resolve feedDir via vault.FeedLocator, since a feed's directory may
// sit at any depth under feeds/. Returns (created, path, error).
func Write(feedDir string, a *Article) (bool, string, error) {
	if err := os.MkdirAll(feedDir, 0o755); err != nil {
		return false, "", err
	}
	// Dedup by the stable id, not the full filename: an article whose
	// title or date was edited (and file renamed) must not be re-fetched
	// on the next sync, since the feed still yields its original name.
	if hits, _ := filepath.Glob(
		filepath.Join(feedDir, "*-"+a.ID()+".md")); len(hits) > 0 {
		return false, hits[0], nil
	}
	path := filepath.Join(feedDir, a.Filename())

	content, err := render(a)
	if err != nil {
		return false, "", err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return false, "", err
	}
	return true, path, nil
}

type Frontmatter struct {
	Title     string `yaml:"title"`
	URL       string `yaml:"url"`
	Published string `yaml:"published"`
	Feed      string `yaml:"feed"`
	GUID      string `yaml:"guid,omitempty"`
}

// ParseFile reads a written article and returns its frontmatter.
func ParseFile(path string) (*Frontmatter, error) {
	fm, _, err := ReadFile(path)
	return fm, err
}

// ReadFile reads a written article and returns its parsed frontmatter
// plus the raw body bytes (everything after the closing --- marker,
// with leading blank lines trimmed).
func ReadFile(path string) (*Frontmatter, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	sep := []byte("---\n")
	if !bytes.HasPrefix(data, sep) {
		return nil, nil, fmt.Errorf("no frontmatter in %s", path)
	}
	fmBody, rest, ok := bytes.Cut(
		data[len(sep):], []byte("\n---\n"))
	if !ok {
		return nil, nil, fmt.Errorf(
			"unterminated frontmatter in %s", path)
	}
	var fm Frontmatter
	if err := yaml.Unmarshal(fmBody, &fm); err != nil {
		return nil, nil, fmt.Errorf(
			"parse frontmatter %s: %w", path, err)
	}
	return &fm, bytes.TrimLeft(rest, "\n"), nil
}

// Fmt re-emits an article with sanitized frontmatter; body preserved.
// Returns (changed, error). No-op when nothing needed cleaning.
func Fmt(path string) (bool, error) {
	fm, body, err := ReadFile(path)
	if err != nil {
		return false, err
	}
	orig := *fm
	fm.Title = textfmt.Line(fm.Title)
	fm.URL = textfmt.Line(fm.URL)
	fm.Feed = textfmt.Line(fm.Feed)
	fm.GUID = textfmt.Line(fm.GUID)
	fm.Published = textfmt.Line(fm.Published)
	if *fm == orig {
		return false, nil
	}
	return true, writeArticleFile(path, fm, body)
}

// Rewrite saves the article at path with the given frontmatter and body
// (body preserved verbatim, frontmatter re-marshaled).
func Rewrite(path string, fm *Frontmatter, body []byte) error {
	return writeArticleFile(path, fm, body)
}

func writeArticleFile(
	path string, fm *Frontmatter, body []byte,
) error {
	fmData, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fmData)
	b.WriteString("---\n\n")
	b.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		b.WriteByte('\n')
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

func render(a *Article) ([]byte, error) {
	fm := Frontmatter{
		Title:     textfmt.Line(a.Title),
		URL:       textfmt.Line(a.URL),
		Published: a.Published.Format(time.RFC3339),
		Feed:      textfmt.Line(a.FeedName),
		GUID:      textfmt.Line(a.GUID),
	}
	fmData, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("marshal Frontmatter: %w", err)
	}

	body := cleanBody(a.Body, a.Title)
	if out, err := markdownfmt.Process("", []byte(body)); err == nil {
		body = strings.TrimRight(string(out), "\n")
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmData)
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// cleanBody strips leading h1 lines from body that don't reference the
// article title (usually feed-level navigation/breadcrumbs), then
// ensures the body opens with `# <title>`.
func cleanBody(body, title string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "# " + title
	}
	for {
		nl := strings.IndexByte(body, '\n')
		first := body
		if nl >= 0 {
			first = body[:nl]
		}
		if !strings.HasPrefix(first, "# ") {
			break
		}
		if containsTitle(first, title) {
			return body
		}
		if nl < 0 {
			body = ""
			break
		}
		body = strings.TrimLeft(body[nl+1:], "\n")
	}
	if body == "" {
		return "# " + title
	}
	return "# " + title + "\n\n" + body
}

func containsTitle(line, title string) bool {
	if title == "" {
		return false
	}
	return strings.Contains(
		strings.ToLower(line), strings.ToLower(title))
}
