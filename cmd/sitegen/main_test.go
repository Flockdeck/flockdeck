package main

import (
	"bytes"
	"html"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// generate writes the site into a temporary directory and returns every page
// in it, by the name of its file.
func generate(t *testing.T) (dir string, pages map[string]string) {
	t.Helper()
	dir = t.TempDir()
	if err := run(dir, defaultRepo, defaultModule, defaultURL); err != nil {
		t.Fatalf("run: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	pages = map[string]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		pages[e.Name()] = string(b)
	}
	return dir, pages
}

// home writes the site and returns the landing page.
func home(t *testing.T) (dir, page string) {
	t.Helper()
	dir, pages := generate(t)
	return dir, pages["index.html"]
}

var (
	idAttr     = regexp.MustCompile(`\sid="([^"]+)"`)
	imgTag     = regexp.MustCompile(`<img\s[^>]*>`)
	ariaRef    = regexp.MustCompile(`\s(?:aria-controls|aria-labelledby)="([^"]+)"`)
	urlAttr    = regexp.MustCompile(`\s(?:src|href)="([^"]*)"`)
	srcsetAttr = regexp.MustCompile(`\ssrcset="([^"]+)"`)
	cssURL     = regexp.MustCompile(`url\(\s*["']?([^"')]+)`)
	// fetchTag is every element a browser fetches something for.
	fetchTag  = regexp.MustCompile(`(?is)<(?:script|img|link|iframe|source|video|audio|embed|object|track)\b[^>]*>`)
	fetchAttr = regexp.MustCompile(`(?i)\s(?:src|href|srcset|data|poster)="([^"]*)"`)
	// elsewhere is an address on another site: one with a scheme, or one
	// that starts with the network path "//".
	elsewhere = regexp.MustCompile(`(?i)^\s*(?:[a-z][a-z0-9+.-]*:|//)`)
)

// Every page is written, each under its own title, description and address,
// which is what a search result or a shared link shows of it.
func TestEveryPageIsWritten(t *testing.T) {
	_, got := generate(t)
	seen := map[string]bool{}
	for _, p := range pages {
		name := p.Path
		if name == "" {
			name = "index.html"
		}
		page, ok := got[name]
		if !ok {
			t.Errorf("%s was not written", name)
			continue
		}
		for _, want := range []string{
			"<title>" + html.EscapeString(p.Title) + "</title>",
			`<meta name="description" content="` + html.EscapeString(p.Description) + `">`,
			`<link rel="canonical" href="` + defaultURL + "/" + p.Path + `">`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s has no %s", name, want)
			}
		}
		for _, s := range []string{p.Title, p.Description} {
			if seen[s] {
				t.Errorf("%s shares %q with another page", name, s)
			}
			seen[s] = true
		}
	}
	// The policy and the terms say when they last changed, which is how a
	// reader who has read them before knows whether to read them again.
	for _, name := range []string{"privacy.html", "terms.html"} {
		if !strings.Contains(got[name], "Last updated: ") {
			t.Errorf("%s does not say when it was last updated", name)
		}
	}
}

// The pages are written by hand in templates, so nothing but a test notices
// an anchor that points nowhere or an id used twice; and the pages link one
// another, so a link to a section of another page has to find it there.
func TestPageReferencesResolve(t *testing.T) {
	dir, pages := generate(t)

	ids := map[string]map[string]bool{}
	for name, page := range pages {
		ids[name] = map[string]bool{}
		for _, m := range idAttr.FindAllStringSubmatch(page, -1) {
			if ids[name][m[1]] {
				t.Errorf("%s: id %q is used twice", name, m[1])
			}
			ids[name][m[1]] = true
		}
	}
	for name, page := range pages {
		for _, m := range ariaRef.FindAllStringSubmatch(page, -1) {
			if !ids[name][m[1]] {
				t.Errorf("%s: %q refers to an element the page does not have", name, strings.TrimSpace(m[0]))
			}
		}
		// A relative address is a file the site has to ship, and so is every
		// candidate a srcset offers.
		refs := []string{}
		for _, m := range urlAttr.FindAllStringSubmatch(page, -1) {
			refs = append(refs, html.UnescapeString(m[1]))
		}
		for _, m := range srcsetAttr.FindAllStringSubmatch(page, -1) {
			for _, cand := range strings.Split(m[1], ",") {
				refs = append(refs, strings.Fields(cand)[0])
			}
		}
		for _, ref := range refs {
			if elsewhere.MatchString(ref) {
				continue
			}
			file, frag, _ := strings.Cut(ref, "#")
			target := name
			switch file {
			case "":
			case "./":
				target = "index.html"
			default:
				target = file
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
					t.Errorf("%s links %s, which the site does not contain", name, file)
					continue
				}
			}
			if frag == "" {
				continue
			}
			if targetIDs, ok := ids[target]; ok && !targetIDs[frag] {
				t.Errorf("%s links %s, but %s has no element with the id %q", name, ref, target, frag)
			}
		}
	}

	// And what the stylesheet asks for: the fonts.
	css, err := os.ReadFile(filepath.Join(dir, "site.css"))
	if err != nil {
		t.Fatal(err)
	}
	fonts := 0
	for _, m := range cssURL.FindAllStringSubmatch(string(css), -1) {
		if elsewhere.MatchString(m[1]) {
			continue // TestNoPageRequestsAnotherOrigin says so
		}
		fonts++
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m[1]))); err != nil {
			t.Errorf("site.css asks for %s, which the site does not contain", m[1])
		}
	}
	if fonts < 2 {
		t.Errorf("site.css asks for %d files of its own; it should serve both fonts", fonts)
	}
}

// The privacy policy says the site loads nothing from anyone else, and a
// font service, a script from a CDN or a stylesheet from another host would
// each tell that host of every visit. A link to another site is fine: nothing
// is fetched until somebody follows it.
func TestNoPageRequestsAnotherOrigin(t *testing.T) {
	dir, pages := generate(t)
	css, err := os.ReadFile(filepath.Join(dir, "site.css"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"site.css": string(css)}
	for name, page := range pages {
		files[name] = page
	}
	for name, body := range files {
		for _, host := range []string{"googleapis", "gstatic"} {
			if strings.Contains(body, host) {
				t.Errorf("%s names %s", name, host)
			}
		}
	}
	for name, page := range pages {
		for _, tag := range fetchTag.FindAllString(page, -1) {
			// A canonical address names the page; nothing fetches it.
			if strings.Contains(tag, `rel="canonical"`) {
				continue
			}
			for _, m := range fetchAttr.FindAllStringSubmatch(tag, -1) {
				for _, cand := range strings.Split(m[1], ",") {
					if elsewhere.MatchString(cand) && !strings.HasPrefix(strings.TrimSpace(cand), "data:") {
						t.Errorf("%s fetches from another site: %s", name, tag)
					}
				}
			}
		}
	}
	for _, m := range cssURL.FindAllStringSubmatch(string(css), -1) {
		if elsewhere.MatchString(m[1]) && !strings.HasPrefix(m[1], "data:") {
			t.Errorf("site.css fetches %s", m[1])
		}
	}
	if strings.Contains(string(css), "@import") {
		t.Error("site.css imports another stylesheet")
	}
}

// Every page ends with the same footer: who made it, under what licence, and
// the way to the privacy policy, the terms and the licences.
func TestEveryPageHasTheFooter(t *testing.T) {
	_, pages := generate(t)
	for name, page := range pages {
		at := strings.Index(page, "<footer")
		if at < 0 {
			t.Errorf("%s has no footer", name)
			continue
		}
		foot := page[at:]
		for _, want := range []string{
			"© 2026 Jim Wright",
			"The desktop app is free and open source under the MIT licence.",
			`href="privacy.html"`, `href="terms.html"`, `href="licences.html"`,
			`#install"`, `#remote"`, `#faq"`,
			`href="` + defaultRepo + `/releases"`,
			`href="` + defaultRepo + `"`,
			`href="` + defaultRepo + `/blob/main/LICENSE"`,
		} {
			if !strings.Contains(foot, want) {
				t.Errorf("%s's footer has no %s", name, want)
			}
		}
	}
}

// latestArchives are the files scripts/publish-downloads.sh puts under
// /latest/ on dl.flockdeck.ai, one for each platform a release is built for,
// with what the install section calls each one.
var latestArchives = map[string]string{
	"flockdeck_windows_amd64.zip":   "Windows (x64)",
	"flockdeck_windows_arm64.zip":   "Windows (Arm)",
	"flockdeck_darwin_arm64.tar.gz": "macOS (Apple silicon)",
	"flockdeck_darwin_amd64.tar.gz": "macOS (Intel)",
	"flockdeck_linux_amd64.tar.gz":  "Linux (x64)",
	"flockdeck_linux_arm64.tar.gz":  "Linux (Arm)",
}

var (
	downloadLink = regexp.MustCompile(`(?s)<a\s[^>]*\bclass="[^"]*\bdl\b[^"]*"[^>]*>(.*?)</a>`)
	anyTag       = regexp.MustCompile(`<[^>]+>`)
)

// Every download button is a link to one of the latest release's archives,
// under the name the publishing script gives it, and every archive has one: a
// button naming a file the script does not upload is a download that fails for
// everybody who presses it.
func TestDownloadButtonsNameTheLatestArchives(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "publish-downloads.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// cmd/release names an archive flockdeck_<version>_<os>_<arch>.<ext>, and
	// the script uploads it to /latest/ with the version taken out.
	const upload = `"latest/flockdeck_${name#flockdeck_"$version"_}"`
	if !strings.Contains(string(script), upload) {
		t.Fatalf("scripts/publish-downloads.sh no longer uploads the archives as %s; bring latestArchives and the buttons into line with what it does", upload)
	}
	for _, p := range licencePlatforms {
		ext := ".tar.gz"
		if p.OS == "windows" {
			ext = ".zip"
		}
		if _, ok := latestArchives["flockdeck_"+p.OS+"_"+p.Arch+ext]; !ok {
			t.Errorf("latestArchives has no archive for %s/%s", p.OS, p.Arch)
		}
	}

	_, page := home(t)
	const base = "https://dl.flockdeck.ai/latest/"
	seen := map[string]bool{}
	for _, m := range downloadLink.FindAllStringSubmatch(page, -1) {
		href := urlAttr.FindStringSubmatch(m[0])
		if href == nil {
			t.Errorf("download button %s has no href", m[0])
			continue
		}
		name, ok := strings.CutPrefix(html.UnescapeString(href[1]), base)
		if !ok {
			t.Errorf("download button %s is not under %s", href[1], base)
			continue
		}
		label, ok := latestArchives[name]
		if !ok {
			t.Errorf("download button %s names %s, which scripts/publish-downloads.sh does not upload", href[1], name)
			continue
		}
		if seen[name] {
			t.Errorf("%s has two download buttons", name)
		}
		seen[name] = true
		if text := strings.TrimSpace(html.UnescapeString(anyTag.ReplaceAllString(m[1], ""))); !strings.HasSuffix(text, label) {
			t.Errorf("the button for %s reads %q, not %q", name, text, label)
		}
	}
	for name := range latestArchives {
		if !seen[name] {
			t.Errorf("the install section has no download button for %s", name)
		}
	}
}

// The site sets no cookies, so it needs no banner: the privacy policy's
// section on cookies is its cookie notice, and has to be there to be linked
// to.
func TestThePrivacyPolicyCarriesTheCookieNotice(t *testing.T) {
	_, pages := generate(t)
	if !strings.Contains(pages["privacy.html"], `id="cookies"`) {
		t.Error("privacy.html has no #cookies to link the cookie notice to")
	}
	for name, page := range pages {
		if strings.Contains(page, "document.cookie") {
			t.Errorf("%s sets a cookie, which the privacy policy says the site never does", name)
		}
	}
}

// licencePlatforms are the platforms a desktop release is built for, as
// cmd/release builds them.
var licencePlatforms = []struct{ OS, Arch string }{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

// The licences page names every module the desktop binary links, on every
// platform a release is built for. cmd/release holds THIRD-PARTY-NOTICES.md
// to the same; this holds the page to it, through the copy it is made from.
func TestLicencesNameEveryLinkedModule(t *testing.T) {
	if testing.Short() {
		t.Skip("asks the go command about every release platform")
	}
	_, pages := generate(t)
	licences := pages["licences.html"]
	root := filepath.Join("..", "..")
	goList := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("go", append([]string{"list"}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	self := goList(nil, "-m")
	for _, p := range licencePlatforms {
		out := goList([]string{"GOOS=" + p.OS, "GOARCH=" + p.Arch, "CGO_ENABLED=0"},
			"-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", ".")
		for _, m := range strings.Fields(out) {
			if m != self && !strings.Contains(licences, m) {
				t.Errorf("the desktop app links %s on %s/%s, which licences.html does not name", m, p.OS, p.Arch)
			}
		}
	}
}

// Every component on the licences page has its licence in full beside it,
// folded away under its name.
func TestEveryComponentHasItsLicenceText(t *testing.T) {
	_, pages := generate(t)
	page := pages["licences.html"]
	heads := regexp.MustCompile(`<h([2-4])[ >]`).FindAllStringSubmatchIndex(page, -1)
	components := 0
	for i, h := range heads {
		if page[h[2]:h[3]] != "4" {
			continue
		}
		components++
		end := len(page)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		if section := page[h[0]:end]; !strings.Contains(section, `<details class="licence-text">`) {
			title, _, _ := strings.Cut(section, "</h4>")
			t.Errorf("%s</h4> has no licence text", title)
		}
	}
	// The desktop's nine and the relay's seven, and the two fonts.
	if components < 18 {
		t.Errorf("the licences page lists %d components", components)
	}
}

// Only the desktop app is open source. The phone client and the relay are a
// proprietary service, so the licences page says so of each and sends
// neither's own licence to the desktop app's MIT licence, and no page offers
// their source or a relay of your own. Enterprise is announced, but only as
// coming, for companies, under licence: "run the relay on your own
// infrastructure" is that announcement, which is why these phrases stop short
// of "your own" alone.
func TestOnlyTheDesktopAppIsOpenSource(t *testing.T) {
	_, pages := generate(t)
	page := pages["licences.html"]
	section := func(id, next string) string {
		from, to := strings.Index(page, `id="`+id+`"`), strings.Index(page, `id="`+next+`"`)
		if from < 0 || to < from {
			t.Fatalf("licences.html has no section %s before %s", id, next)
		}
		return page[from:to]
	}
	if s := section("flockdeck", "desktop"); !strings.Contains(s, "desktop app") || !strings.Contains(s, "MIT licence") {
		t.Error("the licences page does not say the MIT licence is the desktop app's")
	}
	if !strings.Contains(section("desktop", "phone"), `href="#flockdeck"`) {
		t.Error("the desktop app's notices do not lead to its licence")
	}
	for _, s := range []struct{ id, next string }{{"phone", "phone-fonts"}, {"relay", "website"}} {
		body := section(s.id, s.next)
		if !strings.Contains(body, "proprietary") || !strings.Contains(body, "© 2026 Jim Wright") {
			t.Errorf("the licences page's %s section does not say it is proprietary, and whose it is", s.id)
		}
		if strings.Contains(body, `href="#flockdeck"`) {
			t.Errorf("the licences page's %s section sends its own licence to the desktop app's", s.id)
		}
	}
	if !strings.Contains(section("relay", "website"), `href="#relay"`) {
		t.Error("the relay's notices do not lead to the relay's own licence")
	}
	for name, p := range pages {
		for _, claim := range []string{
			`href="https://github.com/jmwri/flockdeck-relay`, `href="https://github.com/jmwri/flockdeck-remote`,
			"run your own", "relay is open source", "relay of your own",
		} {
			if strings.Contains(p, claim) {
				t.Errorf("%s still has %s", name, claim)
			}
		}
	}
}

// Private relays, a relay Flockdeck would run for one paying account, were
// dropped for Enterprise: a licence for companies to run the relay
// themselves. The site announces that as coming, for companies, and still
// lands v0.2.9's Settings link, which goes to #private-relays, on its card.
// No page announces private relays any more.
func TestEnterpriseIsAnnouncedForCompanies(t *testing.T) {
	_, pages := generate(t)
	page := pages["index.html"]
	from := strings.Index(page, `id="enterprise"`)
	if from < 0 {
		t.Fatal("the landing page has no #enterprise card")
	}
	end := strings.Index(page[from:], "</div>")
	if end < 0 {
		t.Fatal("the #enterprise card is not closed")
	}
	// The template wraps its text, so a sentence is looked for with its line
	// breaks put back to spaces.
	card := strings.Join(strings.Fields(page[from:from+end]), " ")
	for _, want := range []string{
		`id="private-relays"`, "coming soon", "for companies", "<h3>Enterprise</h3>",
		"run the relay on your own", "under licence", "with SSO and support",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the #enterprise card does not have %q", want)
		}
	}
	if !strings.Contains(page, `<a href="#enterprise">`) {
		t.Error("the FAQ does not lead to the #enterprise card")
	}
	if !strings.Contains(pages["terms.html"], "A licence for companies to run the relay on their own infrastructure") {
		t.Error("the terms do not say a licence for companies is coming")
	}
	for name, p := range pages {
		if strings.Contains(strings.ToLower(p), "private relay") {
			t.Errorf("%s still announces private relays", name)
		}
	}
}

// What the shared relay, and remote access through it, will cost is not
// settled, so no page may promise that it stays free: the FAQ, the Enterprise
// card, the Settings screenshot's alt text and the terms each did. That the
// app stays free and open source is the MIT licence's promise, and is kept.
func TestNoPagePromisesTheSharedRelayStaysFree(t *testing.T) {
	_, pages := generate(t)
	for name, p := range pages {
		for _, s := range promisesTheRelayStaysFree(p) {
			t.Errorf("%s promises what the shared relay will cost: %q", name, s)
		}
	}
	if !strings.Contains(pages["index.html"], "The app stays free and open source.") {
		t.Error("the FAQ no longer says the app stays free and open source")
	}
}

// pricePromises are the ways a sentence can promise what something will cost
// from now on.
var pricePromises = []string{
	"stays free", "stay free", "remains free", "remain free", "always free", "always be free",
	"free forever", "free for ever", "free for good", "free for life", "never commits you to paying",
}

// promisesTheRelayStaysFree returns each sentence of text that speaks of the
// relay or remote access and promises it a price from now on. A sentence
// about the app alone is left be. Markup ends a sentence too, so a promise in
// an attribute, such as an image's alt text, is found as well as one in prose.
func promisesTheRelayStaysFree(text string) []string {
	var found []string
	for _, s := range regexp.MustCompile(`[.!?<>]`).Split(strings.Join(strings.Fields(text), " "), -1) {
		l := strings.ToLower(s)
		if !strings.Contains(l, "relay") && !strings.Contains(l, "remote") {
			continue
		}
		for _, p := range pricePromises {
			if strings.Contains(l, p) {
				found = append(found, strings.TrimSpace(s))
				break
			}
		}
	}
	return found
}

// The licences page is made from copies of this repository's LICENSE and
// THIRD-PARTY-NOTICES.md, because an embedded file cannot be one outside its
// package's directory. They must stay the same text, or the page would say
// something the releases do not. Line endings are set aside: a Windows
// checkout holds either with CRLF.
func TestLicenceCopiesAreTheOriginals(t *testing.T) {
	for copied, original := range map[string]string{
		"licences/LICENSE":      "LICENSE",
		"licences/flockdeck.md": "THIRD-PARTY-NOTICES.md",
	} {
		c, err := assets.ReadFile("assets/" + copied)
		if err != nil {
			t.Fatal(err)
		}
		o, err := os.ReadFile(filepath.Join("..", "..", original))
		if err != nil {
			t.Fatal(err)
		}
		if string(lf(c)) != string(lf(o)) {
			t.Errorf("cmd/sitegen/assets/%s differs from %s; copy %s over it", copied, original, original)
		}
	}
}

// The site is the same bytes whichever checkout generates it: a Windows
// working tree holds the assets with CRLF, and writing them out that way would
// rewrite every line of the site's repository the next time a Linux machine
// regenerated it.
func TestTextFilesUseLF(t *testing.T) {
	dir, _ := generate(t)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".png") || strings.HasSuffix(path, ".ico") || strings.HasSuffix(path, ".woff2") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "\r\n") {
			t.Errorf("%s is written with CRLF line endings", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An address given with a trailing slash must not put a doubled one into the
// lines a visitor pastes into a terminal, or into any page's links.
func TestTrailingSlashIsNotDoubled(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, defaultRepo+"/", defaultModule+"/", defaultURL+"/"); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, name := range []string{"index.html", "privacy.html", "terms.html", "licences.html"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, base := range []string{defaultURL, defaultRepo} {
			if strings.Contains(string(b), base+"//") {
				t.Errorf("%s writes %s// into an address", name, base)
			}
		}
		// `go install path/@latest` is not a command go accepts.
		if strings.Contains(string(b), defaultModule+"/@latest") {
			t.Errorf("%s writes %s/@latest into the go install line", name, defaultModule)
		}
	}
}

// Every picture needs words for a reader who cannot see it, and a size, so
// that the page does not jump about as each one arrives.
func TestImagesHaveTextAndSize(t *testing.T) {
	_, page := home(t)
	imgs := imgTag.FindAllString(page, -1)
	if len(imgs) == 0 {
		t.Fatal("no images on the page")
	}
	for _, img := range imgs {
		for _, attr := range []string{` alt="`, ` width="`, ` height="`} {
			if !strings.Contains(img, attr) {
				t.Errorf("%s has no%s…\"", img, strings.TrimSuffix(attr, `"`))
			}
		}
	}
}

// Every screenshot is offered at two sizes, and the page has to describe them
// truthfully: a srcset width that is not the file's own has the browser choose
// the wrong one, and a width and height out of proportion to the picture
// reserve the wrong space for it while it loads. Every capture shipped is on
// the page, bar the social card, which is the page's metadata rather than a
// picture on it.
func TestShotsAreOfferedAtBothSizes(t *testing.T) {
	dir, page := home(t)
	offered := map[string]bool{}
	for _, img := range imgTag.FindAllString(page, -1) {
		set := srcsetAttr.FindStringSubmatch(img)
		if set == nil {
			continue
		}
		for _, attr := range []string{` sizes="`, ` loading="lazy"`} {
			if !strings.Contains(img, attr) {
				t.Errorf("%s has no%s", img, strings.TrimSuffix(attr, `"`))
			}
		}
		w, h := intAttr(img, widthAttr), intAttr(img, heightAttr)
		cands := strings.Split(set[1], ",")
		if len(cands) < 2 {
			t.Errorf("%s offers only one size", img)
		}
		for _, cand := range cands {
			f := strings.Fields(cand)
			if len(f) != 2 || !strings.HasSuffix(f[1], "w") {
				t.Errorf("srcset candidate %q is not a file and a width", cand)
				continue
			}
			offered[f[0]] = true
			want, _ := strconv.Atoi(strings.TrimSuffix(f[1], "w"))
			file, err := os.Open(filepath.Join(dir, f[0]))
			if err != nil {
				continue // TestPageReferencesResolve says so
			}
			cfg, err := png.DecodeConfig(file)
			file.Close()
			if err != nil {
				t.Errorf("%s: %v", f[0], err)
				continue
			}
			if cfg.Width != want {
				t.Errorf("%s is %dpx wide, but the srcset calls it %dw", f[0], cfg.Width, want)
			}
			// The same shape as the width and height given, to a pixel.
			if d := cfg.Height*w - cfg.Width*h; d > w || d < -w {
				t.Errorf("%s is %dx%d, out of proportion to the width %d and height %d given for it", f[0], cfg.Width, cfg.Height, w, h)
			}
		}
	}
	shots, err := assets.ReadDir("assets/shots")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range shots {
		if e.Name() != "og.png" && !offered[e.Name()] {
			t.Errorf("assets/shots/%s is shipped, but no picture on the page offers it", e.Name())
		}
	}
}

var (
	widthAttr  = regexp.MustCompile(`\swidth="(\d+)"`)
	heightAttr = regexp.MustCompile(`\sheight="(\d+)"`)
)

// intAttr is the number an attribute holds, or 0.
func intAttr(tag string, attr *regexp.Regexp) int {
	m := attr.FindStringSubmatch(tag)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// The installers are what a visitor pipes straight into a shell, so a syntax
// slip in either reaches every machine that runs the one-liner. dash is the
// strict POSIX shell `curl … | sh` gets on Debian and Ubuntu; PowerShell's own
// parser reads install.ps1 without running it. Either half is skipped where
// its shell is absent.
func TestInstallScriptsParse(t *testing.T) {
	dir, _ := generate(t)

	t.Run("install.sh", func(t *testing.T) {
		sh, err := exec.LookPath("dash")
		if err != nil {
			if sh, err = exec.LookPath("sh"); err != nil {
				t.Skip("no sh to parse it with")
			}
		}
		out, err := exec.Command(sh, "-n", filepath.Join(dir, "install.sh")).CombinedOutput()
		if err != nil {
			t.Errorf("%s -n install.sh: %v\n%s", filepath.Base(sh), err, out)
		}
	})

	t.Run("install.ps1", func(t *testing.T) {
		ps, err := exec.LookPath("pwsh")
		if err != nil {
			if ps, err = exec.LookPath("powershell"); err != nil {
				t.Skip("no PowerShell to parse it with")
			}
		}
		path := strings.ReplaceAll(filepath.Join(dir, "install.ps1"), "'", "''")
		script := "$errs = $null; " +
			"[void][System.Management.Automation.Language.Parser]::ParseFile('" + path + "', [ref]$null, [ref]$errs); " +
			"if ($errs) { $errs | ForEach-Object { $_.ToString() }; exit 1 }"
		out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
		if err != nil {
			t.Errorf("parsing install.ps1: %v\n%s", err, out)
		}
	})
}

// The favicon is the application's own icon, copied rather than drawn again
// (main.go says why), so the two must stay the same picture. Line endings are
// set aside: a Windows checkout holds either file with CRLF.
func TestFaviconIsTheAppIcon(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(lf(icon)) != string(lf(app)) {
		t.Error("cmd/sitegen/assets/favicon.svg differs from internal/webui/assets/icon.svg; copy the app's icon over it")
	}
	ico, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(iconICO, ico) {
		t.Error("cmd/sitegen/assets/favicon.ico differs from internal/webui/assets/icon.ico; copy the app's icon over it")
	}
	touch, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon-256.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(touchIcon, touch) {
		t.Error("cmd/sitegen/assets/apple-touch-icon.png differs from internal/webui/assets/icon-256.png; copy the app's icon over it")
	}
}

// Every page names all three icons, and each is written beside it: the SVG
// for browsers that show one, favicon.ico for those that do not and for
// whatever asks for /favicon.ico unprompted, and the home-screen icon. All
// three are the application's own icons (TestFaviconIsTheAppIcon).
//
// What is checked is what was written, not what was embedded. The site's
// text files are all written with LF, and an image passed through that loses
// every CR in it: a PNG's signature holds one, and favicon.ico held four. Both
// were written broken before this looked at the files themselves.
func TestEveryPageHasItsIcons(t *testing.T) {
	dir, pages := generate(t)
	written := map[string][]byte{}
	for _, name := range []string{"favicon.svg", "favicon.ico", "apple-touch-icon.png"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("the site has no %s: %v", name, err)
			continue
		}
		written[name] = body
	}
	if !bytes.Equal(written["favicon.ico"], iconICO) {
		t.Errorf("favicon.ico was written as %d bytes, not the %d it is", len(written["favicon.ico"]), len(iconICO))
	}
	if !bytes.Equal(written["apple-touch-icon.png"], touchIcon) {
		t.Errorf("apple-touch-icon.png was written as %d bytes, not the %d it is", len(written["apple-touch-icon.png"]), len(touchIcon))
	}
	if len(pages) == 0 {
		t.Fatal("the site has no pages")
	}
	for page, body := range pages {
		for _, link := range []string{
			`<link rel="icon" href="favicon.ico" sizes="32x32">`,
			`<link rel="icon" href="favicon.svg" type="image/svg+xml">`,
			`<link rel="apple-touch-icon" href="apple-touch-icon.png">`,
		} {
			if !strings.Contains(body, link) {
				t.Errorf("%s does not name its icon: %s", page, link)
			}
		}
	}
	if _, err := png.Decode(bytes.NewReader(written["apple-touch-icon.png"])); err != nil {
		t.Errorf("apple-touch-icon.png as written: %v", err)
	}
}
