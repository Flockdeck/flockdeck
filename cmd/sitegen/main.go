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
//	go run ./cmd/sitegen -out ../flockdeck-site -release v1.2.3 -checksums checksums.txt
//
// -release is the release the install scripts install by default, and
// -checksums that release's checksums.txt, from
// https://dl.flockdeck.ai/v1.2.3/checksums.txt, with its checksums.txt.sig
// beside it: the signature is checked against the release keys built into
// this program, the primary or the standby, before anything in the file is
// trusted. The SHA-256 of each archive is written into the scripts, which
// check the archive they download against it; see bake.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/selfupdate"
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

// iconICO is the same icon for whatever asks for /favicon.ico on its own, as
// browsers, feed readers and bookmark tools do whether or not a page names it,
// and for a browser that shows no SVG in its tabs. It too is the application's
// own, internal/webui/assets/icon.ico, copied — a multi-resolution ICO (16
// through 256px) so Windows never has to stretch one small bitmap to a size
// it does not have.
//
//go:embed assets/favicon.ico
var iconICO []byte

// touchIcon is the icon a phone puts on its home screen: the application's
// own internal/webui/assets/icon-180.png, copied, like the other two — 180px
// is what iOS actually asks an apple-touch-icon for.
//
//go:embed assets/apple-touch-icon.png
var touchIcon []byte

const (
	defaultRepo = "https://github.com/Flockdeck/flockdeck"
	// defaultModule stays on the old owner: it must match go.mod's own module
	// line exactly, which this repository has not renamed (unlike
	// flockdeck-remote), so `go install` from the site would resolve nothing
	// under a path that only the GitHub repository itself has moved to.
	defaultModule = "github.com/jmwri/flockdeck"
	defaultURL    = "https://flockdeck.ai"

	// downloadsURL is where releases are published, which the install
	// section's download buttons link to: scripts/publish-downloads.sh puts
	// the latest release's archives under /latest/ there.
	downloadsURL = "https://dl.flockdeck.ai"

	// sponsorURL is where the #sponsor section sends somebody who wants to
	// give something back: the GitHub Sponsors account .github/FUNDING.yml
	// names, which a test keeps this and the app's own link level with.
	sponsorURL = "https://github.com/sponsors/jmwri"
)

func main() {
	out := flag.String("out", "site", "`directory` to write the site into")
	repo := flag.String("repo", defaultRepo, "`url` of the source repository")
	module := flag.String("module", defaultModule, "`path` the module is installed from")
	url := flag.String("url", defaultURL, "`address` the site is served from, which the install lines name")
	version := flag.String("release", "", "the release the install scripts install by default, such as v1.2.3 (`version`)")
	sums := flag.String("checksums", "", "`path` to that release's checksums.txt, with its checksums.txt.sig beside it")
	flag.Parse()

	rel, err := readRelease(*version, *sums, selfupdate.TrustedKeys())
	if err == nil {
		err = run(*out, *repo, *module, *url, rel)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sitegen:", err)
		os.Exit(1)
	}
}

// release is the release the install scripts are generated for: its version,
// and the SHA-256 of each of its archives by the archive's name.
type release struct {
	Version string
	Sums    map[string]string
}

// releaseVersion is what a release's tag looks like. It is written into both
// scripts inside quotes, so nothing that could end them gets through.
var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// sha256Hex is a SHA-256 as checksums.txt gives it.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// archives are the archives of version the install scripts download, named as
// cmd/release names them.
func archives(version string) []string {
	var names []string
	for _, p := range []string{"darwin_amd64.tar.gz", "darwin_arm64.tar.gz", "linux_amd64.tar.gz", "linux_arm64.tar.gz", "windows_amd64.zip", "windows_arm64.zip"} {
		names = append(names, "flockdeck_"+version+"_"+p)
	}
	return names
}

// readRelease is the release version, with the checksums the checksums.txt
// at path gives its archives, once the signature beside it at path+".sig" is
// found to be one of keys': keys are this build's trusted release keys, the
// primary and the standby (selfupdate.TrustedKeys).
//
// Neither the version nor the checksums may be left out. Without them the
// scripts would have nothing to check an archive against but a checksums.txt
// fetched from wherever the archive came from, and whoever could replace the
// one there could replace the other with it.
//
// The signature is checked here rather than left to whoever runs this. These
// checksums are what every install from the site trusts in place of anything
// it downloads, and a step done by hand is a step that can be skipped.
func readRelease(version, path string, keys []ed25519.PublicKey) (release, error) {
	if version == "" || path == "" {
		return release{}, errors.New("-release and -checksums are both required: the install scripts check the archive they install against the SHA-256 that release's checksums.txt gives it, written into them here; download it from " + downloadsURL + "/<version>/checksums.txt, with checksums.txt.sig beside it")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return release{}, fmt.Errorf("read the checksums: %w", err)
	}
	sig, err := os.ReadFile(path + ".sig")
	if err != nil {
		return release{}, fmt.Errorf("read the checksums' signature: %w; download checksums.txt.sig from %s/%s/ and put it beside them", err, downloadsURL, version)
	}
	if err := selfupdate.VerifyAny(keys, body, sig); err != nil {
		return release{}, fmt.Errorf("%s.sig: %w, so the checksums are not the release's", path, err)
	}
	return parseRelease(version, body)
}

// parseRelease reads a checksums.txt as cmd/release writes it, a SHA-256 and a
// file name to a line, and fails unless it gives every archive of version one.
func parseRelease(version string, sums []byte) (release, error) {
	if !releaseVersion.MatchString(version) {
		return release{}, fmt.Errorf("-release %q is not a release's tag, such as v1.2.3", version)
	}
	rel := release{Version: version, Sums: map[string]string{}}
	for _, line := range strings.Split(string(lf(sums)), "\n") {
		if f := strings.Fields(line); len(f) == 2 && sha256Hex.MatchString(f[0]) {
			rel.Sums[f[1]] = f[0]
		}
	}
	for _, name := range archives(version) {
		if rel.Sums[name] == "" {
			return release{}, fmt.Errorf("the checksums give no SHA-256 for %s: are they %s's?", name, version)
		}
	}
	return rel, nil
}

// bake writes rel into an install script: its version in place of @RELEASE@,
// and in place of @RELEASE_SUMS@ the lines of checksums.txt for its archives
// that end in suffix, the ones that script can download. Each has to be found
// exactly once, so a script that stops naming them fails here rather than
// going out with nothing to check against.
func bake(name string, script []byte, rel release, suffix string) ([]byte, error) {
	var sums strings.Builder
	sums.WriteString("\n")
	for _, a := range archives(rel.Version) {
		if strings.HasSuffix(a, suffix) {
			fmt.Fprintf(&sums, "%s  %s\n", rel.Sums[a], a)
		}
	}
	for _, r := range []struct{ from, to string }{{"@RELEASE@", rel.Version}, {"@RELEASE_SUMS@", sums.String()}} {
		if n := bytes.Count(script, []byte(r.from)); n != 1 {
			return nil, fmt.Errorf("%s says %s %d times, not once", name, r.from, n)
		}
		script = bytes.Replace(script, []byte(r.from), []byte(r.to), 1)
	}
	return script, nil
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
	// Sponsor is where a visitor can sponsor Flockdeck, and Sponsors are
	// those who asked to be named for it, from assets/sponsors.txt.
	Sponsor  string
	Sponsors []sponsor
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
		Title:       "Flockdeck | Coding agents on hardware you control",
		Description: "Flockdeck runs Claude Code, Codex, Gemini or your own model on hardware you already own, self-hosted or on your desk, and reaches your phone with no port opened. Free and open source, for Windows, macOS and Linux.",
		Social:      "Run your coding agents on hardware you control, and reach them from your phone."},
	{Path: "trust.html", Template: "doc.html.tmpl", Source: "trust.md",
		Title:       "Trust & privacy | Flockdeck",
		Description: "What Flockdeck collects (almost nothing, structurally, not as a paid mode), what isn't end-to-end encrypted yet, and how that compares to the rest of the market.",
		Social:      "Nothing to opt out of: what Flockdeck collects, what it doesn't, and how that compares."},
	{Path: "privacy.html", Template: "doc.html.tmpl", Source: "privacy.md",
		Title:       "Privacy policy | Flockdeck",
		Description: "What personal data the Flockdeck desktop app, the Flockdeck relay and this website involve, who else is involved, and your rights over it."},
	{Path: "terms.html", Template: "doc.html.tmpl", Source: "terms.md",
		Title:       "Terms of service | Flockdeck",
		Description: "The terms for using the shared Flockdeck relay at remote.flockdeck.ai, and this website."},
	{Path: "licences.html", Template: "licences.html.tmpl",
		Title:       "Licences | Flockdeck",
		Description: "The Flockdeck desktop app's licence, and every third-party component in the desktop app, the phone client and the relay, each with its licence in full."},
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

func run(out, repo, module, url string, rel release) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create the output directory: %w", err)
	}

	// The template appends "/install.sh" and the like to both addresses, and
	// "@latest" to the module path, so a slash given at the end of any of them
	// would print a doubled one into an install line, or a go install line go
	// does not accept.
	s := site{Repo: strings.TrimRight(repo, "/"), Module: strings.TrimRight(module, "/"), URL: strings.TrimRight(url, "/"), Downloads: downloadsURL, Sponsor: sponsorURL}
	sponsors, err := loadSponsors()
	if err != nil {
		return err
	}
	s.Sponsors = sponsors

	// Every file but the pages is gathered first: the pages link the assets by
	// their fingerprinted names, which are known only once the assets are.
	files := map[string][]byte{}
	css, err := assets.ReadFile("assets/site.css")
	if err != nil {
		return err
	}
	files["favicon.svg"] = icon
	for name, suffix := range map[string]string{"install.sh": ".tar.gz", "install.ps1": ".zip"} {
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		if files[name], err = bake(name, body, rel, suffix); err != nil {
			return err
		}
	}
	// The fonts are served from a folder of their own, with their licences
	// beside them as the licence asks.
	binary := map[string][]byte{}
	// The raster icons are images, and go with the binary files: every text
	// file is written with LF below, which takes every CR out of an image
	// along with it. A PNG's signature holds one.
	binary["favicon.ico"] = iconICO
	binary["apple-touch-icon.png"] = touchIcon
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

	// The fingerprinted names. The fonts come first, because the stylesheet
	// names them, and its own fingerprint has to cover the names it gives.
	names := map[string]string{}
	for name, body := range binary {
		if !servedByName[name] {
			names[name] = fingerprint(name, body)
		}
	}
	css = lf(css)
	for name, hashed := range names {
		if strings.HasPrefix(name, "fonts/") {
			css = bytes.ReplaceAll(css, []byte(`"`+name+`"`), []byte(`"`+hashed+`"`))
		}
	}
	names["site.css"] = fingerprint("site.css", css)
	files["site.css"] = css

	pageFiles, err := render(s, names)
	if err != nil {
		return err
	}
	for name, body := range pageFiles {
		files[name] = lf(body)
	}
	for name, body := range binary {
		files[name] = body
	}

	// What the pages being replaced link is read before they are.
	previous, err := linked(out)
	if err != nil {
		return err
	}
	written := map[string]bool{}
	for plain, body := range files {
		name := plain
		if hashed, ok := names[plain]; ok {
			name = hashed
		}
		path := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create the folder for %s: %w", name, err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		written[name] = true
	}
	if err := prune(out, names, written, previous); err != nil {
		return err
	}

	fmt.Printf("wrote the site to %s\n", out)
	return nil
}

// lf is text with Windows line endings made plain ones.
func lf(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// servedByName are the binary files asked for by a name of their own rather
// than one a page gives: browsers, feed readers and bookmark tools ask for
// /favicon.ico whether or not a page names it, and iOS for
// /apple-touch-icon.png. Every other stylesheet, font and image is served
// under a fingerprinted name. (favicon.svg is a text file, and keeps its name
// too: the site's CI asks for it by name.)
//
// og.png is the picture a link to the site shows in Slack, Discord or X, and
// the pages name it by its plain address, which nginx serves no-cache. Those
// services keep the address a link gave them and fetch the picture from it
// again when their copy expires. A fingerprinted address would be taken out
// two regenerations after the picture next changed, and every link shared
// before that would lose its picture.
var servedByName = map[string]bool{
	"favicon.ico":          true,
	"apple-touch-icon.png": true,
	"og.png":               true,
}

// fingerprint is name with the start of its content's SHA-256 put before the
// extension: site.css becomes site.3f9c2a1b7e.css.
//
// A changed file gets a new name, so a browser holding the old one can never
// show it in place of the new: the site's nginx serves these names as
// immutable, for a year, and the pages that name them as no-cache, so a visit
// after a deploy asks for the new pages and, through them, the new files.
// Nobody has to clear a cache to see an update.
func fingerprint(name string, body []byte) string {
	sum := sha256.Sum256(body)
	ext := path.Ext(name)
	return strings.TrimSuffix(name, ext) + "." + hex.EncodeToString(sum[:])[:10] + ext
}

// hashedName matches a name fingerprint gives.
var hashedName = regexp.MustCompile(`\.[0-9a-f]{10}\.[a-z0-9]+$`)

// hashedFiles are the fingerprinted files in out, by the names a page links
// them by.
func hashedFiles(out string) ([]string, error) {
	var found []string
	for _, dir := range []string{"", "fonts"} {
		entries, err := os.ReadDir(filepath.Join(out, dir))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if name := path.Join(dir, e.Name()); !e.IsDir() && hashedName.MatchString(name) {
				found = append(found, name)
			}
		}
	}
	return found, nil
}

// linked is every fingerprinted file in out that the pages already there
// link, or that the pages as out's repository last committed them link, and
// that the stylesheet they link asks for in turn: the generation of the site
// before this one, and the generation being served.
//
// prune leaves these for one more run. A page that was open before a deploy
// goes on asking for its own generation's files after it: a lazy screenshot
// scrolled to, or another srcset candidate once the window is resized. And
// while a deploy rolls, a page served by a server still on the old image can
// send its requests for its files to one already on the new. Were the old
// files gone, each of those would be a 404 on a page that works.
//
// The committed pages count as well as the ones on disk because the site is
// deployed from its commits, not from the working tree. Two regenerations
// before a commit, a release tried twice say, would otherwise take each
// other's pages as the generation before, and the second would take out the
// files the site being served still links.
func linked(out string) (map[string]bool, error) {
	hashed, err := hashedFiles(out)
	if err != nil {
		return nil, err
	}
	var texts [][]byte
	for _, p := range pages {
		name := p.Path
		if name == "" {
			name = "index.html"
		}
		if body, ok := committed(out, name); ok {
			texts = append(texts, body)
		}
		body, err := os.ReadFile(filepath.Join(out, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read the %s being replaced: %w", name, err)
		}
		texts = append(texts, body)
	}
	keep := map[string]bool{}
	mark := func() {
		for _, name := range hashed {
			for _, text := range texts {
				if bytes.Contains(text, []byte(name)) {
					keep[name] = true
					break
				}
			}
		}
	}
	mark()
	// The fonts are named by the stylesheet, not by the pages.
	for name := range keep {
		if path.Ext(name) != ".css" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil {
			return nil, fmt.Errorf("read the %s being replaced: %w", name, err)
		}
		texts = append(texts, body)
	}
	mark()
	return keep, nil
}

// committed is the file name in out as out's repository last committed it,
// and false where out is not a repository, git is not installed, or the last
// commit has no such file: then there is no committed generation to keep.
// "HEAD:./" names the file relative to out, which need not be the top of its
// repository.
func committed(out, name string) ([]byte, bool) {
	body, err := exec.Command("git", "-C", out, "show", "HEAD:./"+name).Output()
	return body, err == nil
}

// prune takes out of out what a run before this one wrote and this one did
// not: a fingerprinted file whose content has changed since, and the plain
// name an asset had before it was fingerprinted. out is the site's own
// repository, and a file left there is served for as long as it stays.
//
// A fingerprinted file the pages this run replaced still linked, previous, is
// the exception (see linked): it stays for this run, and goes on the next,
// when nothing links it any more.
func prune(out string, names map[string]string, written, previous map[string]bool) error {
	for plain := range names {
		if err := os.Remove(filepath.Join(out, filepath.FromSlash(plain))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove the old %s: %w", plain, err)
		}
	}
	hashed, err := hashedFiles(out)
	if err != nil {
		return err
	}
	for _, name := range hashed {
		if written[name] || previous[name] {
			continue
		}
		if err := os.Remove(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("remove the stale %s: %w", name, err)
		}
	}
	return nil
}

// render fills every page in, by the name of the file it is written to.
//
// mark is a function rather than a field because the wordmark is markup, and
// passing it as data would mean either escaping it into visible angle brackets
// or handing the template an unescaped string and hoping. A function returning
// template.HTML says once, here, that this particular markup is ours.
//
// asset is the name a file is served under, which for most of them carries
// its content's fingerprint (see fingerprint). A page asks for "site.css" and
// is given "site.3f9c2a1b7e.css"; asking for a file that has no fingerprinted
// name fails the build, rather than linking a file that is not there.
func render(s site, names map[string]string) (map[string][]byte, error) {
	layout, err := template.New("layout").Funcs(template.FuncMap{
		"mark": func() template.HTML { return wordmark },
		"asset": func(name string) (string, error) {
			if hashed, ok := names[name]; ok {
				return hashed, nil
			}
			return "", fmt.Errorf("%s has no fingerprinted name: it is not one of the site's assets, or it is served under its own name", name)
		},
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
	// what it is: the desktop app's is the PolyForm Noncommercial licence at
	// the top, and the phone client's and the relay's are proprietary, as
	// their sections say.
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
// PolyForm Noncommercial licence at the top, so the phone client's and the
// relay's, which are proprietary, must not be sent there.
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
