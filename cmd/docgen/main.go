// Command docgen writes docs.flockdeck.ai: the "using Flockdeck" section is
// rendered straight from internal/help, the same Markdown and the same
// key-table placeholders that render behind F1 in the app, so a web copy of
// it cannot say anything the app itself does not — the one thing
// flockdeck-site's own sitegen deliberately declined to do, because a second
// copy of the help would be a second version to keep honest. Here it isn't
// a second version: it's the same one, generated again.
//
// The self-hosting section has no such source inside the app — there is
// nothing self-hosting a relay to render placeholders from — so it is
// ordinary Markdown, written by hand and kept in the docs repository itself,
// under content/self-hosting. This program only ever reads that directory;
// it never writes into it.
//
// Usage, from a checkout of this repository, beside a checkout of
// github.com/jmwri/flockdeck-docs:
//
//	go run ./cmd/docgen -out ../flockdeck-docs
package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

//go:embed assets/*.tmpl assets/docs.css assets/favicon.svg assets/favicon.ico assets/apple-touch-icon.png assets/fonts
var assets embed.FS

const (
	defaultURL  = "https://docs.flockdeck.ai"
	defaultRepo = "https://github.com/jmwri/flockdeck-docs"
)

// selfHostOrder is the sequence the self-hosting pages are written in, for
// the same reason internal/help/help.go writes one out for the app pages: a
// table of contents has an argument to make, and the file system's own order
// does not make it.
var selfHostOrder = []string{
	"overview",
	"requirements",
	"deploying-the-relay",
	"configuration",
	"storage-and-data",
	"admin-and-invites",
	"connecting-desktops",
	"security-and-privacy",
	"upgrading",
	"troubleshooting",
}

func main() {
	out := flag.String("out", "", "`directory` to write the docs site into (a checkout of flockdeck-docs)")
	content := flag.String("content", "", "`directory` the self-hosting Markdown lives in (default: <out>/content/self-hosting)")
	url := flag.String("url", defaultURL, "`address` the site is served from")
	repo := flag.String("repo", defaultRepo, "`url` of this docs repository")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "docgen: -out is required")
		os.Exit(1)
	}
	if *content == "" {
		*content = filepath.Join(*out, "content", "self-hosting")
	}
	if err := run(*out, *content, *url, *repo); err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

// navItem is one entry in the sidebar and on the home page.
type navItem struct {
	Href, Title string
}

// navSection is one group of the sidebar: "Using Flockdeck" or
// "Self-hosting".
type navSection struct {
	Title string
	Items []navItem
}

// site is what the pages need to know about where they are served.
type site struct {
	URL, Repo string
}

// page is what a page's template is given.
type page struct {
	site
	Nav         []navSection
	Active      string // the sidebar href this page matches, e.g. "app/panes.html"
	Section     string // "Using Flockdeck" or "Self-hosting", shown above the title
	Title       string
	Description string
	Body        template.HTML
}

func (p page) Canonical() string { return p.URL + "/" + p.Active }

func run(out, contentDir, url, repo string) error {
	appPages, err := help.Pages()
	if err != nil {
		return fmt.Errorf("render the app's help pages: %w", err)
	}
	selfPages, err := loadSelfHosting(contentDir)
	if err != nil {
		return fmt.Errorf("load self-hosting pages: %w", err)
	}

	nav := []navSection{
		{Title: "Using Flockdeck", Items: navItems("app", appPagesAsDocs(appPages))},
		{Title: "Self-hosting", Items: navItems("self-hosting", selfPages)},
	}
	s := site{URL: strings.TrimRight(url, "/"), Repo: strings.TrimRight(repo, "/")}

	layout, err := template.New("layout").Funcs(template.FuncMap{"mark": func() template.HTML { return wordmark }}).
		ParseFS(assets, "assets/layout.html.tmpl")
	if err != nil {
		return fmt.Errorf("parse the layout: %w", err)
	}

	files := map[string][]byte{}

	// The two page sections, each rendered with the same page template.
	for _, sec := range []struct {
		dir   string
		title string
		pages []docPage
	}{
		{"app", "Using Flockdeck", appPagesAsDocs(appPages)},
		{"self-hosting", "Self-hosting", selfPages},
	} {
		for _, p := range sec.pages {
			href := sec.dir + "/" + p.slug + ".html"
			body, err := render(layout, "page.html.tmpl", page{
				site: s, Nav: nav, Active: href, Section: sec.title,
				Title:       p.title + " | Flockdeck docs",
				Description: p.summary,
				Body:        template.HTML(p.html),
			})
			if err != nil {
				return fmt.Errorf("render %s: %w", href, err)
			}
			files[href] = body
		}
	}

	home, err := render(layout, "home.html.tmpl", page{
		site: s, Nav: nav, Active: "",
		Title:       "Flockdeck docs",
		Description: "How to use Flockdeck, the desktop app that runs Claude Code, Codex, Gemini and other coding agents side by side, and how to self-host its relay for remote access.",
	})
	if err != nil {
		return fmt.Errorf("render the home page: %w", err)
	}
	files["index.html"] = home

	css, err := assets.ReadFile("assets/docs.css")
	if err != nil {
		return err
	}
	files["docs.css"] = css

	binary := map[string][]byte{}
	for _, name := range []string{"favicon.svg", "favicon.ico", "apple-touch-icon.png"} {
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		binary[name] = body
	}
	fonts, err := assets.ReadDir("assets/fonts")
	if err != nil {
		return err
	}
	for _, e := range fonts {
		body, err := assets.ReadFile("assets/fonts/" + e.Name())
		if err != nil {
			return err
		}
		if strings.HasSuffix(e.Name(), ".txt") {
			files["fonts/"+e.Name()] = body
		} else {
			binary["fonts/"+e.Name()] = body
		}
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create the output directory: %w", err)
	}
	for name, body := range files {
		if err := write(out, name, lf(body)); err != nil {
			return err
		}
	}
	for name, body := range binary {
		if err := write(out, name, body); err != nil {
			return err
		}
	}

	fmt.Printf("wrote the docs site to %s: %d app pages, %d self-hosting pages\n", out, len(appPages), len(selfPages))
	return nil
}

func write(out, name string, body []byte) error {
	path := filepath.Join(out, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create the folder for %s: %w", name, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func navItems(dir string, pages []docPage) []navItem {
	out := make([]navItem, len(pages))
	for i, p := range pages {
		out[i] = navItem{Href: dir + "/" + p.slug + ".html", Title: p.title}
	}
	return out
}

func render(layout *template.Template, name string, data page) ([]byte, error) {
	t, err := layout.Clone()
	if err != nil {
		return nil, err
	}
	if _, err := t.ParseFS(assets, "assets/"+name); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// docPage is one page, either an app help page or a self-hosting one, in the
// shape the templates need: a slug for its address, a title for the nav and
// <title>, a one-line summary for its meta description, and its body already
// rendered to HTML.
type docPage struct {
	slug, title, summary, html string
}

func appPagesAsDocs(pages []help.Page) []docPage {
	crossPage := crossPageLinks(pages)
	out := make([]docPage, len(pages))
	for i, p := range pages {
		out[i] = docPage{slug: p.Slug, title: p.Title, summary: p.Summary, html: ledeParagraph(crossPage.ReplaceAllString(p.HTML, `href="/app/$1.html"`))}
	}
	return out
}

// crossPageLinks matches the in-app help's own cross-references, written as
// `[text](#slug)` for the hash-routed single page the application renders.
// Split across a file per slug the way this site is, those hrefs have to
// become `/app/slug.html` instead, or they'd try to scroll to an element
// that was never on the page to begin with.
func crossPageLinks(pages []help.Page) *regexp.Regexp {
	slugs := make([]string, len(pages))
	for i, p := range pages {
		slugs[i] = regexp.QuoteMeta(p.Slug)
	}
	return regexp.MustCompile(`href="#(` + strings.Join(slugs, "|") + `)"`)
}

// selfHostMD renders the self-hosting pages: plain Markdown, with tables for
// the configuration reference and fenced code blocks for the deployment
// commands.
var selfHostMD = goldmark.New(
	goldmark.WithExtensions(extension.Table),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
)

// loadSelfHosting reads every page in selfHostOrder from dir. A page listed
// there with no file behind it is an error rather than a silently thin
// contents list; a file in dir not listed there is the same, so a page
// cannot be added and forgotten by the nav that is supposed to reach it.
func loadSelfHosting(dir string) ([]docPage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	present := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			present[strings.TrimSuffix(e.Name(), ".md")] = true
		}
	}
	for slug := range present {
		if !contains(selfHostOrder, slug) {
			return nil, fmt.Errorf("%s.md is in %s but not in selfHostOrder", slug, dir)
		}
	}
	out := make([]docPage, 0, len(selfHostOrder))
	for _, slug := range selfHostOrder {
		if !present[slug] {
			return nil, fmt.Errorf("selfHostOrder names %s but %s/%s.md does not exist", slug, dir, slug)
		}
		src, err := os.ReadFile(filepath.Join(dir, slug+".md"))
		if err != nil {
			return nil, err
		}
		title, summary := titleAndSummary(string(src))
		if title == "" {
			return nil, fmt.Errorf("%s.md: no top-level heading", slug)
		}
		var b bytes.Buffer
		if err := selfHostMD.Convert(lf(src), &b); err != nil {
			return nil, fmt.Errorf("render %s.md: %w", slug, err)
		}
		out = append(out, docPage{slug: slug, title: title, summary: summary, html: ledeParagraph(b.String())})
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// titleAndSummary reads a page's own first heading and first paragraph, the
// way internal/help does for the app pages, so every page's nav title and
// meta description come from the page rather than beside it.
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
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|") ||
			strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			break
		}
		summary = append(summary, trimmed)
	}
	return title, plainMarkdown(strings.Join(summary, " "))
}

var mdEmphasis = regexp.MustCompile("[`*_]")
var mdLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// plainMarkdown strips the handful of inline marks a summary line might
// carry, so a meta description doesn't show literal asterisks or backticks.
func plainMarkdown(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	return mdEmphasis.ReplaceAllString(s, "")
}

// ledeAfterH1 matches the first paragraph right after a page's top heading,
// however goldmark happens to have spaced the two tags.
var ledeAfterH1 = regexp.MustCompile(`(?s)(</h1>\s*)<p>`)

// ledeParagraph marks the paragraph right after a page's <h1> so the
// stylesheet can set it in the lede size, the way flockdeck-site's doc pages
// do for the privacy policy and the terms. A page with no such paragraph
// right after its heading is left as it is.
func ledeParagraph(htmlText string) string {
	return ledeAfterH1.ReplaceAllString(htmlText, `${1}<p class="doc-lede">`)
}

// wordmark is the mark at 20px, the same geometry as flockdeck-site's.
const wordmark template.HTML = `<svg viewBox="0 0 32 32" width="20" height="20" aria-hidden="true">` +
	`<rect fill="#93A1A9" x="3" y="22" width="26" height="2.6" rx="1.3"/>` +
	`<circle fill="#93A1A9" cx="8.4" cy="18" r="3.4"/>` +
	`<circle fill="#93A1A9" cx="23.6" cy="18" r="3.4"/>` +
	`<path fill="#4FD1DB" d="M16 3.4L21 12.2H11Z"/>` +
	`</svg>`

// lf is text with Windows line endings made plain ones, so the site is the
// same bytes whichever machine generates it.
func lf(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
