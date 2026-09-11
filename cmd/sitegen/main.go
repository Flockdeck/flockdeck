// Command sitegen writes the public landing page.
//
// The page is generated rather than written by hand so that what it claims
// about Flockdeck comes from the repository it is built in: the agents it
// names and the install line sit next to the code that makes them true. The
// help itself stays in the application, behind F1, where it is rendered with
// the real key bindings substituted into it.
//
// The markup and the stylesheet live beside this file as real .html and .css
// rather than as string constants in Go. They are long enough, and enough of
// what they do is visual, that editing them wants an editor that knows what
// they are; the few things that come from the repository are template actions.
//
// The output is plain files with no build step, because the site is served as
// a container image built from a repository of its own:
//
//	go run ./cmd/sitegen -out ../flockdeck-site -shot shot.png
package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
)

// The install scripts are served from the site so the line a visitor copies is
// short and names the product's own domain. They are kept here rather than in
// the site's repository because they carry the archive names cmd/release
// writes, and the two should change in the same commit.
//
//go:embed assets/index.html.tmpl assets/site.css assets/install.sh assets/install.ps1
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
)

func main() {
	out := flag.String("out", "site", "`directory` to write the site into")
	shot := flag.String("shot", "", "`screenshot` to copy in and show on the page")
	repo := flag.String("repo", defaultRepo, "`url` of the source repository")
	module := flag.String("module", defaultModule, "`path` the module is installed from")
	url := flag.String("url", defaultURL, "`address` the site is served from, which the install lines name")
	flag.Parse()

	if err := run(*out, *shot, *repo, *module, *url); err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}

// site is what the page needs to know about where it came from.
type site struct {
	Repo   string
	Module string
	Shot   string
	// URL is where the site itself is served, which the install lines have to
	// name in full: they are pasted into a terminal, not followed as links.
	URL string
}

func run(out, shot, repo, module, url string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create the output directory: %w", err)
	}

	s := site{Repo: repo, Module: module, URL: url}
	if shot != "" {
		s.Shot = filepath.Base(shot)
	}

	page, err := render(s)
	if err != nil {
		return err
	}

	css, err := assets.ReadFile("assets/site.css")
	if err != nil {
		return err
	}

	files := map[string][]byte{
		"index.html":  page,
		"site.css":    css,
		"favicon.svg": icon,
		// Pages runs what it is given through Jekyll unless it is told not to,
		// and Jekyll hides every path beginning with an underscore. There is
		// nothing here for it to do.
		".nojekyll": nil,
	}
	// The scripts are run straight off the wire, and a checkout on Windows can
	// hand them over with CRLF endings, which sh takes as part of every
	// command. They are written out with LF whatever the working tree had.
	for _, name := range []string{"install.sh", "install.ps1"} {
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		files[name] = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	}

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(out, name), body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	if shot != "" {
		data, err := os.ReadFile(shot)
		if err != nil {
			return fmt.Errorf("read the screenshot: %w", err)
		}
		if err := os.WriteFile(filepath.Join(out, s.Shot), data, 0o644); err != nil {
			return fmt.Errorf("write the screenshot: %w", err)
		}
	}

	fmt.Printf("wrote the site to %s\n", out)
	return nil
}

// render fills the page in.
//
// mark is a function rather than a field because the wordmark is markup, and
// passing it as data would mean either escaping it into visible angle brackets
// or handing the template an unescaped string and hoping. A function returning
// template.HTML says once, here, that this particular markup is ours.
func render(s site) ([]byte, error) {
	t, err := template.New("index.html.tmpl").Funcs(template.FuncMap{
		"mark": func() template.HTML { return wordmark },
	}).ParseFS(assets, "assets/index.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse the page: %w", err)
	}

	var b bytes.Buffer
	if err := t.Execute(&b, s); err != nil {
		return nil, fmt.Errorf("render the page: %w", err)
	}
	return b.Bytes(), nil
}

// wordmark is the mark at 20px, inline so the header does not wait on a
// request to draw its own name. The geometry is the icon's.
const wordmark template.HTML = `<svg viewBox="0 0 32 32" width="20" height="20" aria-hidden="true">` +
	`<rect fill="#93A1A9" x="3" y="22" width="26" height="2.6" rx="1.3"/>` +
	`<circle fill="#93A1A9" cx="8.4" cy="18" r="3.4"/>` +
	`<circle fill="#93A1A9" cx="23.6" cy="18" r="3.4"/>` +
	`<path fill="#4FD1DB" d="M16 3.4L21 12.2H11Z"/>` +
	`</svg>`
