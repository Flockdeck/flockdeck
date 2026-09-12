// Command sitegen writes the public site: the landing page, and beside it the
// privacy policy, the terms and the licences.
//
// The site is generated rather than written by hand so that what it claims
// about Flockdeck comes from the repository it is built in: the agents it
// names and the install line sit next to the code that makes them true. The
// help itself stays in the application, behind F1, where it is rendered with
// the real key bindings substituted into it.
//
// The markup and the stylesheet live beside this file as real .html and .css
// rather than as string constants in Go. They are long enough, and enough of
// what they do is visual, that editing them wants an editor that knows what
// they are; the few things that come from the repository are template actions.
// Every page draws its head, header and footer from layout.html.tmpl, so the
// fonts, the stylesheet and the footer's links are written once.
//
// The privacy policy and the terms are Markdown, rendered here, because they
// are prose that is edited as prose, a word at a time, by somebody reading it
// as a whole. The licences page is built from the notices files themselves,
// so that it says what the releases carry and cannot say anything else.
//
// The output is plain files with no build step, because the site is served as
// a container image built from a repository of its own:
//
//	go run ./cmd/sitegen -out ../flockdeck-site
package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The install scripts are served from the site so the line a visitor copies is
// short and names the product's own domain. They are kept here rather than in
// the site's repository because they carry the archive names cmd/release
// writes, and the two should change in the same commit.
//
// The screenshots are staged captures of the real application. They live
// here too, so the site builds from the repository alone rather than from
// whatever image the person running this happens to have lying around.
//
// The fonts are flockdeck-remote's web/app/fonts, the same files the phone
// client serves, with their licences.
//
// assets/licences holds what the licences page is made of: this repository's
// LICENSE and THIRD-PARTY-NOTICES.md, which a test keeps the same as the
// originals at the top of the repository, and the notices of flockdeck-remote
// and flockdeck-relay, copied from those repositories. When either of theirs
// changes, copy it over the one here.
//
//go:embed assets/*.tmpl assets/*.md assets/site.css assets/install.sh assets/install.ps1 assets/shots/*.png assets/fonts assets/licences
var assets embed.FS

// icon is the application's own mark, which becomes the site's favicon.
//
// It is copied from internal/webui/assets rather than drawn again here: the
// deck with two agents at rest and one raised is the same picture in the tab
// strip, in the taskbar and on the page, and a second copy would be a second
// thing to keep in step.
//
//go:embed assets/favicon.svg
var icon []byte

const (
	defaultRepo   = "https://github.com/jmwri/flockdeck"
	defaultModule = "github.com/jmwri/flockdeck"
	defaultURL    = "https://flockdeck.ai"

	// downloadsURL is where releases are published, which the install
	// section's download buttons link to: scripts/publish-downloads.sh puts
	// the latest release's archives under /latest/ there.
	downloadsURL = "https://dl.flockdeck.ai"
)

func main() {
	out := flag.String("out", "site", "`directory` to write the site into")
	repo := flag.String("repo", defaultRepo, "`url` of the source repository")
	module := flag.String("module", defaultModule, "`path` the module is installed from")
	url := flag.String("url", defaultURL, "`address` the site is served from, which the install lines name")
	flag.Parse()

	if err := run(*out, *repo, *module, *url); err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}

// site is what the pages need to know about where they came from.
type site struct {
	Repo   string
	Module string
	// URL is where the site itself is served, which the install lines have to
	// name in full: they are pasted into a terminal, not followed as links.
	URL string
	// Downloads is where the release archives are published.
	Downloads string
}

// pages are the site's pages: the file each is written to, the template that
// draws it, the Markdown it is rendered from if any, and what a search result
// or a shared link says of it. The home page's path is empty, because its
// address is the site's own.
var pages = []struct {
	Path, Template, Source string
	Title, Description     string
	Social                 string
}{
	{Path: "", Template: "index.html.tmpl",
		Title:       "Flockdeck | Run all your coding agents at once",
		Description: "Flockdeck runs Claude Code, Codex, Gemini and other coding agents side by side, and shows you which one needs your input. For Windows, macOS and Linux.",
		Social:      "Run all your coding agents side by side and see which one needs you."},
	{Path: "privacy.html", Template: "doc.html.tmpl", Source: "privacy.md",
		Title:       "Privacy policy | Flockdeck",
		Description: "What personal data the Flockdeck desktop app, the Flockdeck relay and this website involve, who else is involved, and your rights over it."},
	{Path: "terms.html", Template: "doc.html.tmpl", Source: "terms.md",
		Title:       "Terms of service | Flockdeck",
		Description: "The terms for using the shared Flockdeck relay at remote.flockdeck.ai, and this website."},
	{Path: "licences.html", Template: "licences.html.tmpl",
		Title:       "Licences | Flockdeck",
		Description: "The Flockdeck desktop app's MIT licence, and every third-party component in the desktop app, the phone client and the relay, each with its licence in full."},
}

// page is what a page's template is given: where the site came from, and
// which page it is drawing.
type page struct {
	site
	// Path is the file the page is written to, and what its address ends
	// in; empty for the home page.
	Path        string
	Title       string
	Description string
	// Social is what a link shared somewhere says of the page, where that is
	// shorter than the description.
	Social string
	// Body is the page's Markdown, rendered, and Contents its sections.
	Body     template.HTML
	Contents []heading
	Licences *licences
}

// Canonical is the page's own address.
func (p page) Canonical() string { return p.URL + "/" + p.Path }

// Home is what a link to a section of the home page starts with: nothing on
// the home page itself, and the home page's address from any other.
func (p page) Home() string {
	if p.Path == "" {
		return ""
	}
	return "./"
}

// Summary is what a shared link says of the page.
func (p page) Summary() string {
	if p.Social != "" {
		return p.Social
	}
	return p.Description
}

// heading is a section of a long page, as its contents list it.
type heading struct{ ID, Text string }

func run(out, repo, module, url string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create the output directory: %w", err)
	}

	// The template appends "/install.sh" and the like to both addresses, and
	// "@latest" to the module path, so a slash given at the end of any of them
	// would print a doubled one into an install line, or a go install line go
	// does not accept.
	s := site{Repo: strings.TrimRight(repo, "/"), Module: strings.TrimRight(module, "/"), URL: strings.TrimRight(url, "/"), Downloads: downloadsURL}

	files, err := render(s)
	if err != nil {
		return err
	}

	css, err := assets.ReadFile("assets/site.css")
	if err != nil {
		return err
	}
	files["site.css"] = css
	files["favicon.svg"] = icon
	for _, name := range []string{"install.sh", "install.ps1"} {
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		files[name] = body
	}
	// The fonts are served from a folder of their own, with their licences
	// beside them as the licence asks.
	binary := map[string][]byte{}
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
	// Every text file is written with LF whatever the working tree had. A
	// Windows checkout hands the assets over with CRLF, which sh takes as part
	// of every command in install.sh; and the site should be the same bytes
	// whichever machine generates it, or regenerating it from another checkout
	// rewrites every line of the site's repository.
	for name, body := range files {
		files[name] = lf(body)
	}
	// The screenshots are served beside the page, not from a folder of their own.
	shots, err := assets.ReadDir("assets/shots")
	if err != nil {
		return err
	}
	for _, e := range shots {
		if binary[e.Name()], err = assets.ReadFile("assets/shots/" + e.Name()); err != nil {
			return err
		}
	}
	for name, body := range binary {
		files[name] = body
	}

	for name, body := range files {
		path := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create the folder for %s: %w", name, err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	fmt.Printf("wrote the site to %s\n", out)
	return nil
}

// lf is text with Windows line endings made plain ones.
func lf(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// render fills every page in, by the name of the file it is written to.
//
// mark is a function rather than a field because the wordmark is markup, and
// passing it as data would mean either escaping it into visible angle brackets
// or handing the template an unescaped string and hoping. A function returning
// template.HTML says once, here, that this particular markup is ours.
func render(s site) (map[string][]byte, error) {
	layout, err := template.New("layout").Funcs(template.FuncMap{
		"mark": func() template.HTML { return wordmark },
	}).ParseFS(assets, "assets/layout.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse the layout: %w", err)
	}

	out := map[string][]byte{}
	for _, p := range pages {
		name := p.Path
		if name == "" {
			name = "index.html"
		}
		t, err := layout.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := t.ParseFS(assets, "assets/"+p.Template); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p.Template, err)
		}
		data := page{site: s, Path: p.Path, Title: p.Title, Description: p.Description, Social: p.Social}
		if p.Source != "" {
			if data.Body, data.Contents, err = prose(p.Source); err != nil {
				return nil, err
			}
		}
		if p.Template == "licences.html.tmpl" {
			if data.Licences, err = loadLicences(); err != nil {
				return nil, err
			}
			data.Contents = licenceSections
		}
		var b bytes.Buffer
		if err := t.ExecuteTemplate(&b, p.Template, data); err != nil {
			return nil, fmt.Errorf("render %s: %w", name, err)
		}
		out[name] = b.Bytes()
	}
	return out, nil
}

// proseMD renders the privacy policy and the terms. Every heading is given an
// id, so that any section can be linked to: the privacy policy's #cookies is
// the site's cookie notice, and needs no banner, the site setting no cookies.
var proseMD = goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID()))

// prose renders a Markdown page, and lists its sections for the contents.
func prose(name string) (template.HTML, []heading, error) {
	src, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return "", nil, err
	}
	src = lf(src)
	doc := proseMD.Parser().Parse(text.NewReader(src))
	var contents []heading
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Level != 2 {
			continue
		}
		id, _ := h.AttributeString("id")
		b, _ := id.([]byte)
		contents = append(contents, heading{ID: string(b), Text: plainText(h, src)})
	}
	var b bytes.Buffer
	if err := proseMD.Renderer().Render(&b, src, doc); err != nil {
		return "", nil, fmt.Errorf("render %s: %w", name, err)
	}
	return template.HTML(b.String()), contents, nil
}

// plainText is the words of a heading, without its markup.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// licences is what the licences page shows.
type licences struct {
	// Own is Flockdeck's LICENSE.
	Own string
	// Desktop, Phone and Relay are the THIRD-PARTY-NOTICES.md of the desktop
	// app, the phone client and the relay, rendered.
	Desktop, Phone, Relay template.HTML
	// Fonts are the two typefaces' licences, which the phone client's
	// notices name as the files beside the fonts rather than reproducing.
	Fonts []fontLicence
}

type fontLicence struct{ Name, File, Text string }

// licenceSections are the licences page's contents.
var licenceSections = []heading{
	{"flockdeck", "The desktop app's licence"},
	{"desktop", "The desktop app"},
	{"phone", "The phone client"},
	{"relay", "The relay"},
	{"website", "This website"},
	{"agents", "Agents and model APIs"},
}

func loadLicences() (*licences, error) {
	read := func(name string) ([]byte, error) {
		b, err := assets.ReadFile("assets/" + name)
		return lf(b), err
	}
	own, err := read("licences/LICENSE")
	if err != nil {
		return nil, err
	}
	l := &licences{Own: string(own)}
	// Each notices file's own licence is the section of the page that says
	// what it is: the desktop app's is the MIT licence at the top, and the
	// phone client's and the relay's are proprietary, as their sections say.
	for _, n := range []struct {
		file, licence string
		into          *template.HTML
	}{
		{"licences/flockdeck.md", "#flockdeck", &l.Desktop},
		{"licences/flockdeck-remote.md", "#phone", &l.Phone},
		{"licences/flockdeck-relay.md", "#relay", &l.Relay},
	} {
		src, err := read(n.file)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := noticesMD(n.licence).Convert(src, &b); err != nil {
			return nil, fmt.Errorf("render %s: %w", n.file, err)
		}
		*n.into = template.HTML(b.String())
	}
	for _, f := range []struct{ name, file string }{
		{"Archivo", "OFL-Archivo.txt"},
		{"JetBrains Mono", "OFL-JetBrainsMono.txt"},
	} {
		body, err := read("fonts/" + f.file)
		if err != nil {
			return nil, err
		}
		l.Fonts = append(l.Fonts, fontLicence{Name: f.name, File: "fonts/" + f.file, Text: string(body)})
	}
	return l, nil
}

// noticesMD renders a notices file into the licences page, where licence is
// the page's anchor for the file's own licence.
//
// Each file is written to stand on its own, so its title gives way to the
// page's heading for it and the rest steps down a level beneath that. Each
// licence text is folded away under its component, which keeps the page a
// list of names and holders that can be read down, rather than twenty-odd
// screens of legal text with the names lost between them.
func noticesMD(licence string) goldmark.Markdown {
	return goldmark.New(
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(noticesShape{licence}, 100))),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(foldedText{}, 100))),
	)
}

// noticesShape fits a notices file under the page's heading for it. Its
// links to its own repository's LICENSE go to licence instead, the section of
// the page that says what that licence is: the file on its own sits beside
// that LICENSE, and on the page it does not. Only the desktop app's is the
// MIT licence at the top, so the phone client's and the relay's, which are
// proprietary, must not be sent there.
type noticesShape struct{ licence string }

func (s noticesShape) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	var title ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			if n.Level == 1 && title == nil {
				title = n
			} else if n.Level < 6 {
				n.Level++
			}
		case *ast.Link:
			if string(n.Destination) == "LICENSE" {
				n.Destination = []byte(s.licence)
			}
		}
		return ast.WalkContinue, nil
	})
	if title != nil {
		title.Parent().RemoveChild(title.Parent(), title)
	}
}

// foldedText draws a licence text, which is what every code block in a
// notices file is, folded under a summary that opens it.
type foldedText struct{}

func (foldedText) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		_, _ = w.WriteString(`<details class="licence-text"><summary>Full licence text</summary><pre><code>`)
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			_, _ = w.WriteString(html.EscapeString(string(seg.Value(src))))
		}
		_, _ = w.WriteString("</code></pre></details>\n")
		return ast.WalkSkipChildren, nil
	})
}

// wordmark is the mark at 20px, inline so the header does not wait on a
// request to draw its own name. The geometry is the icon's.
const wordmark template.HTML = `<svg viewBox="0 0 32 32" width="20" height="20" aria-hidden="true">` +
	`<rect fill="#93A1A9" x="3" y="22" width="26" height="2.6" rx="1.3"/>` +
	`<circle fill="#93A1A9" cx="8.4" cy="18" r="3.4"/>` +
	`<circle fill="#93A1A9" cx="23.6" cy="18" r="3.4"/>` +
	`<path fill="#4FD1DB" d="M16 3.4L21 12.2H11Z"/>` +
	`</svg>`
