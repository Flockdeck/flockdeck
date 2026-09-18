// Package help holds the application's help pages. They are written as
// Markdown, compiled into the binary alongside the front end, and rendered
// once on first use; the interface fetches the result and shows it.
//
// Shortcuts are not written into the prose by hand. Pages refer to actions by
// id — `[[key:splitRight]]` inline, `{{keys:Panes}}` for a table — and those
// are expanded from the one table in keys.go, which the command palette reads
// as well. A binding therefore cannot be changed in one place and left stale
// in another.
package help

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
)

//go:embed pages/*.md
var pagesFS embed.FS

// order is the sequence the pages are listed in. It is written out rather
// than sorted because a table of contents has an argument to make, and
// alphabetical order does not make it. A test keeps it level with the files
// actually present.
var order = []string{
	"getting-started",
	"panes",
	"agents",
	"status",
	"spend",
	"rearranging",
	"broadcast",
	"fanout",
	"todo",
	"worktrees",
	"changes",
	"github",
	"history",
	"projects",
	"persistence",
	"remote",
	"settings",
	"shortcuts",
	"cli",
	"troubleshooting",
}

// Page is one help page, rendered.
type Page struct {
	Slug string `json:"slug"`
	// Title is the page's first heading.
	Title string `json:"title"`
	// Summary is its first paragraph, shown under the title in the contents.
	Summary string `json:"summary"`
	// HTML is the rendered page.
	HTML string `json:"html"`
	// Text is the same content as words only, which is what search matches.
	Text string `json:"text"`
}

var (
	once    sync.Once
	loaded  []Page
	loadErr error
)

// Pages returns every help page, in reading order. The result is rendered on
// the first call and reused; the content is compiled in, so it cannot change
// underneath us.
func Pages() ([]Page, error) {
	once.Do(func() { loaded, loadErr = render() })
	return loaded, loadErr
}

// Slugs returns the page identifiers, in reading order.
func Slugs() []string {
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// render renders every page in order. A slug with no file behind it fails in
// renderPage, naming the page; TestOrderMatchesFiles catches it before then.
func render() ([]Page, error) {
	pages := make([]Page, 0, len(order))
	for _, slug := range order {
		p, err := renderPage(slug)
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, nil
}

func renderPage(slug string) (Page, error) {
	src, err := fs.ReadFile(pagesFS, "pages/"+slug+".md")
	if err != nil {
		return Page{}, fmt.Errorf("help: read %s: %w", slug, err)
	}
	expanded, err := expand(string(src))
	if err != nil {
		return Page{}, fmt.Errorf("help: %s: %w", slug, err)
	}
	title, summary := titleAndSummary(string(src))
	if title == "" {
		return Page{}, fmt.Errorf("help: %s: no top-level heading", slug)
	}
	// The summary is scraped out of the source, not the rendered page, and the
	// contents list shows it as text — so a placeholder, a <kbd> or a pair of
	// asterisks would all arrive on screen exactly as written. Expand the
	// shortcuts to bare keys, then render and flatten it like any other prose.
	summary, err = summaryText(summary)
	if err != nil {
		return Page{}, fmt.Errorf("help: %s: summary: %w", slug, err)
	}
	htmlText, err := toHTML(expanded)
	if err != nil {
		return Page{}, fmt.Errorf("help: render %s: %w", slug, err)
	}
	return Page{
		Slug:    slug,
		Title:   title,
		Summary: summary,
		HTML:    htmlText,
		Text:    plainText(htmlText),
	}, nil
}

// summaryText turns one paragraph of page source into the plain sentence the
// contents list shows.
func summaryText(src string) (string, error) {
	expanded, err := expandInlineKeysWith(src, func(keys string) string { return keys })
	if err != nil {
		return "", err
	}
	expanded, err = expandActionsWith(expanded, func(keys string) string { return keys })
	if err != nil {
		return "", err
	}
	htmlText, err := toHTML(expanded)
	if err != nil {
		return "", err
	}
	return plainText(htmlText), nil
}

// titleAndSummary reads the page's own first heading and first paragraph, so
// that the contents list is written in the page rather than beside it.
func titleAndSummary(src string) (string, string) {
	var title string
	var summary []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if title == "" {
			if strings.HasPrefix(trimmed, "# ") {
				title = strings.TrimSpace(trimmed[2:])
			}
			continue
		}
		if trimmed == "" {
			if len(summary) > 0 {
				break
			}
			continue
		}
		// A page whose first block is a heading or a list has no summary
		// paragraph; do not scrape one out of the wrong thing.
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|") ||
			strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			break
		}
		summary = append(summary, trimmed)
	}
	return title, strings.Join(summary, " ")
}

// fileSlugs lists the pages present on disk.
func fileSlugs() ([]string, error) {
	entries, err := fs.ReadDir(pagesFS, "pages")
	if err != nil {
		return nil, fmt.Errorf("help: read pages: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(out)
	return out, nil
}
