package selfupdate

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// place is a server standing in for the site or for GitHub: it serves exactly
// the files it is given, answers 404 for anything else, and remembers every
// request it was sent.
type place struct {
	srv *httptest.Server

	mu     sync.Mutex
	files  map[string][]byte
	status map[string]int // an answer other than the file, by path
	broken bool           // drop every connection without answering
	seen   []*http.Request
}

func newPlace(t *testing.T) *place {
	t.Helper()
	p := &place{files: map[string][]byte{}, status: map[string]int{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r)
		body, ok := p.files[r.URL.Path]
		code, forced := p.status[r.URL.Path]
		broken := p.broken
		p.mu.Unlock()
		switch {
		case broken:
			// Out of reach, without depending on how quickly a closed port
			// refuses: the connection is taken and closed unanswered.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		case forced:
			http.Error(w, http.StatusText(code), code)
		case !ok:
			http.NotFound(w, r)
		default:
			w.Write(body)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *place) set(path string, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[path] = body
}

func (p *place) get(path string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.files[path]
}

func (p *place) remove(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.files, path)
}

func (p *place) fail(path string, code int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status[path] = code
}

func (p *place) breakAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.broken = true
}

func (p *place) requests() []*http.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*http.Request(nil), p.seen...)
}

// asked reports whether any request was for a path starting with prefix.
func (p *place) asked(prefix string) bool {
	for _, r := range p.requests() {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return true
		}
	}
	return false
}

// release is v9.9.9 published the way the release workflow publishes it: on
// the site, with its manifest signed and latest.json naming it, and on
// GitHub, with checksums.txt.sig beside its checksums.txt.
type release struct {
	dl, gh   *place
	key      ed25519.PrivateKey
	name     string
	archive  []byte
	manifest Manifest
}

const ghDownloads = "/" + Repo + "/releases/download/v9.9.9/"

// published puts v9.9.9, holding body as the program, on a site and a GitHub
// of the test's own, points the updater at both, and has it trust a key of the
// test's own in place of the release key.
func published(t *testing.T, body string) *release {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &release{dl: newPlace(t), gh: newPlace(t), key: key}
	r.name, r.archive = buildArchive(t, body)

	oldKey, oldStandby, oldSite, oldAPI, oldGH := trustedKey, trustedStandbyKey, siteURL, githubAPIURL, githubURL
	trustedKey, siteURL, githubAPIURL, githubURL = pub, r.dl.srv.URL, r.gh.srv.URL, r.gh.srv.URL
	t.Cleanup(func() {
		trustedKey, trustedStandbyKey, siteURL, githubAPIURL, githubURL = oldKey, oldStandby, oldSite, oldAPI, oldGH
	})

	sums := []byte(fmt.Sprintf("%s  %s\n", sha(r.archive), r.name))
	sumsSig := Sign(key, sums)
	for name, data := range map[string][]byte{r.name: r.archive, sumsName: sums, sumsName + sigExt: sumsSig} {
		r.dl.set("/v9.9.9/"+name, data)
		r.manifest.Files = append(r.manifest.Files, ManifestFile{
			Name: name, URL: r.dl.srv.URL + "/v9.9.9/" + name, SHA256: sha(data), Size: int64(len(data)),
		})
		r.gh.set(ghDownloads+name, data)
	}
	r.manifest.Version = "v9.9.9"
	r.manifest.Date = time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	r.manifest.NotesURL = "https://github.com/" + Repo + "/releases/tag/v9.9.9"
	r.manifest.Notes = "what changed"
	r.sign()
	r.point("v9.9.9")
	r.onGitHub("v9.9.9", r.name, sumsName, sumsName+sigExt)
	return r
}

// onGitHub has GitHub's API give tag as the latest release, holding the named
// files of v9.9.9 as GitHub serves them.
func (r *release) onGitHub(tag string, names ...string) {
	assets := []map[string]any{}
	for _, n := range names {
		assets = append(assets, map[string]any{"name": n, "browser_download_url": r.gh.srv.URL + ghDownloads + n})
	}
	api, err := json.Marshal(map[string]any{
		"tag_name": tag, "body": "what changed on GitHub", "html_url": r.manifest.NotesURL, "assets": assets,
	})
	if err != nil {
		panic(err) // strings and slices of them always marshal
	}
	r.gh.set("/repos/"+Repo+"/releases/latest", api)
}

// sign puts the manifest as it stands on the site, with its signature, where
// v9.9.9's belongs.
func (r *release) sign() { r.signAt("v9.9.9") }

// signAt puts the manifest as it stands on the site, with its signature, where
// version's belongs.
func (r *release) signAt(version string) {
	data, err := json.MarshalIndent(r.manifest, "", "  ")
	if err != nil {
		panic(err) // a struct of strings, numbers and a time always marshals
	}
	r.dl.set("/"+version+"/manifest.json", data)
	r.dl.set("/"+version+"/manifest.json.sig", Sign(r.key, data))
}

// point has the site's latest.json name version, as the publish script
// writes it.
func (r *release) point(version string) {
	data, err := json.Marshal(Pointer{Version: version})
	if err != nil {
		panic(err)
	}
	r.dl.set("/latest.json", append(data, '\n'))
}

// addVersion publishes another version, distinct from v9.9.9, holding the
// same archive body already built for r so a test does not need to build a
// second one. onSite puts it on the download site the way `published` put
// v9.9.9 there, except latest.json is left alone -- it is meant to be named
// directly, not found through it. onGH puts it on GitHub by tag, the way
// Fetch's GitHub fallback reads one.
func (r *release) addVersion(t *testing.T, version string, onSite, onGH bool) {
	t.Helper()
	sums := []byte(fmt.Sprintf("%s  %s\n", sha(r.archive), r.name))
	sumsSig := Sign(r.key, sums)
	notesURL := "https://github.com/" + Repo + "/releases/tag/" + version

	if onSite {
		m := Manifest{Version: version, Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotesURL: notesURL, Notes: "notes for " + version}
		for name, data := range map[string][]byte{r.name: r.archive, sumsName: sums, sumsName + sigExt: sumsSig} {
			r.dl.set("/"+version+"/"+name, data)
			m.Files = append(m.Files, ManifestFile{
				Name: name, URL: r.dl.srv.URL + "/" + version + "/" + name, SHA256: sha(data), Size: int64(len(data)),
			})
		}
		data, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		r.dl.set("/"+version+"/manifest.json", data)
		r.dl.set("/"+version+"/manifest.json.sig", Sign(r.key, data))
	}

	if onGH {
		dl := "/" + Repo + "/releases/download/" + version + "/"
		for name, data := range map[string][]byte{r.name: r.archive, sumsName: sums, sumsName + sigExt: sumsSig} {
			r.gh.set(dl+name, data)
		}
		var assets []map[string]any
		for _, n := range []string{r.name, sumsName, sumsName + sigExt} {
			assets = append(assets, map[string]any{"name": n, "browser_download_url": r.gh.srv.URL + dl + n})
		}
		api, err := json.Marshal(map[string]any{
			"tag_name": version, "body": "notes for " + version + " on GitHub", "html_url": notesURL, "assets": assets,
		})
		if err != nil {
			t.Fatal(err)
		}
		r.gh.set("/repos/"+Repo+"/releases/tags/"+version, api)
	}
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// logged collects what the updater logs, for the test to read.
func logged(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var b strings.Builder
	old := logf
	logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(&b, format+"\n", args...)
	}
	t.Cleanup(func() { logf = old })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return b.String()
	}
}

// staged is the program Stage put in dir, or fails the test.
func staged(t *testing.T, p *Pending) string {
	t.Helper()
	got, err := os.ReadFile(p.Binary)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// The site is asked first: latest.json for the version, then that version's
// manifest, believed once its signature checks. The archive comes from the
// site too: GitHub is not asked anything, and nothing is logged.
func TestLatestAndStageUseTheSignedSite(t *testing.T) {
	r := published(t, "the new program")
	log := logged(t)

	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "v9.9.9" || rel.Notes != "what changed" || rel.URL != r.manifest.NotesURL {
		t.Errorf("Latest = %s %q %s; want the release its manifest describes", rel.Version, rel.Notes, rel.URL)
	}
	if got := rel.DownloadSize(); got != int64(len(r.archive)) {
		t.Errorf("DownloadSize = %d, want the size the manifest gives, %d", got, len(r.archive))
	}
	for _, path := range []string{"/latest.json", "/v9.9.9/manifest.json", "/v9.9.9/manifest.json.sig"} {
		if !r.dl.asked(path) {
			t.Errorf("%s was not read from the site", path)
		}
	}
	p, err := Stage(context.Background(), rel, t.TempDir())
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if got := staged(t, p); got != "the new program" {
		t.Errorf("staged %q", got)
	}
	if !r.dl.asked("/v9.9.9/" + r.name) {
		t.Error("the archive was not downloaded from the site")
	}
	if reqs := r.gh.requests(); len(reqs) > 0 {
		t.Errorf("GitHub was asked for %s with the site answering", reqs[0].URL.Path)
	}
	if l := log(); l != "" {
		t.Errorf("logged with nothing wrong:\n%s", l)
	}
}

// Whatever keeps the site's answer from being used, the updater goes to
// GitHub as every release before the site did, and says why. A signature that
// does not check, or a signed manifest served as another version's, is a
// warning: the site served something that was not released. latest.json is not
// signed, so a latest.json the updater cannot use is only a note.
func TestLatestFallsBackToGitHub(t *testing.T) {
	cases := []struct {
		name    string
		break_  func(r *release)
		says    string
		warning bool
	}{
		{"out of reach", func(r *release) { r.dl.breakAll() }, "could not read the latest release", false},
		{"an error", func(r *release) { r.dl.fail("/latest.json", http.StatusServiceUnavailable) }, "503", false},
		{"no latest.json", func(r *release) { r.dl.remove("/latest.json") }, "404", false},
		{"a latest.json that is a page", func(r *release) {
			r.dl.set("/latest.json", []byte("<html>maintenance</html>"))
		}, "latest.json does not name a release", false},
		{"a latest.json naming no version", func(r *release) { r.point("latest") }, "not a version", false},
		{"a latest.json naming a pre-release", func(r *release) {
			r.manifest.Version = "v9.9.10-rc.1"
			r.signAt("v9.9.10-rc.1")
			r.point("v9.9.10-rc.1")
		}, "a pre-release", false},
		{"a latest.json naming a release the site does not have", func(r *release) { r.point("v9.9.10") }, "404", false},
		{"no signature", func(r *release) { r.dl.remove("/v9.9.9/manifest.json.sig") }, "404", false},
		{"a manifest that is a page", func(r *release) {
			r.dl.set("/v9.9.9/manifest.json", []byte("<html>maintenance</html>"))
		}, "not signed by the release key", true},
		{"a manifest changed after signing", func(r *release) {
			r.dl.set("/v9.9.9/manifest.json", []byte(strings.Replace(string(r.dl.get("/v9.9.9/manifest.json")), "what changed", "what did not", 1)))
		}, "not signed by the release key", true},
		{"signed by another key", func(r *release) {
			_, other, _ := ed25519.GenerateKey(nil)
			r.dl.set("/v9.9.9/manifest.json.sig", Sign(other, r.dl.get("/v9.9.9/manifest.json")))
		}, "not signed by the release key", true},
		{"another version's signed manifest", func(r *release) {
			r.manifest.Version = "v9.9.8"
			r.sign()
		}, "signed for v9.9.8", true},
		{"a signed manifest naming no version", func(r *release) {
			r.manifest.Version = "latest"
			r.sign()
		}, "not a version", false},
		{"a signed manifest without checksums.txt.sig", func(r *release) {
			r.manifest.Files = r.manifest.Files[:0]
			r.sign()
		}, "does not list", false},
		{"no release key in this build", func(r *release) { trustedKey, trustedStandbyKey = nil, nil }, "no release key", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := published(t, "the new program")
			log := logged(t)
			c.break_(r)

			rel, err := Latest(context.Background())
			if err != nil {
				t.Fatalf("Latest: %v", err)
			}
			if rel.Notes != "what changed on GitHub" {
				t.Errorf("Latest = %+v, want GitHub's release", rel)
			}
			l := log()
			if !strings.Contains(l, c.says) || !strings.Contains(l, "using GitHub instead") {
				t.Errorf("logged %q, want it to say %q and that GitHub is used", l, c.says)
			}
			if strings.Contains(l, "warning:") != c.warning {
				t.Errorf("logged %q; a warning = %v, want %v", l, !c.warning, c.warning)
			}
			p, err := Stage(context.Background(), rel, t.TempDir())
			if err != nil || staged(t, p) != "the new program" {
				t.Fatalf("Stage from GitHub: %v", err)
			}
		})
	}
}

// Once the manifest has been believed, the archive and its checksums are
// fetched from the site, and anything wrong with them sends the download to
// GitHub instead, with the reason logged: a warning when what the site served
// was not what was released.
func TestStageFallsBackToGitHub(t *testing.T) {
	cases := []struct {
		name    string
		break_  func(r *release)
		warning bool
	}{
		{"the archive gone", func(r *release) { r.dl.remove("/v9.9.9/" + r.name) }, false},
		{"checksums.txt unsigned", func(r *release) { r.dl.remove("/v9.9.9/checksums.txt.sig") }, false},
		{"checksums.txt signed by another key", func(r *release) {
			_, other, _ := ed25519.GenerateKey(nil)
			r.dl.set("/v9.9.9/checksums.txt.sig", Sign(other, r.dl.get("/v9.9.9/checksums.txt")))
		}, true},
		{"a tampered archive", func(r *release) {
			_, evil := buildArchive(t, "a program nobody released")
			r.dl.set("/v9.9.9/"+r.name, evil)
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := published(t, "the new program")
			log := logged(t)
			rel, err := Latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			c.break_(r)

			dir := t.TempDir()
			p, err := Stage(context.Background(), rel, dir)
			if err != nil {
				t.Fatalf("Stage: %v", err)
			}
			if got := staged(t, p); got != "the new program" {
				t.Errorf("staged %q, want the release from GitHub", got)
			}
			if !r.gh.asked(ghDownloads + r.name) {
				t.Error("the archive was not fetched from GitHub")
			}
			if r.gh.asked("/repos/") {
				t.Error("GitHub's API was asked, where the signed manifest had already named the release")
			}
			l := log()
			if !strings.Contains(l, "could not download v9.9.9") || strings.Contains(l, "warning:") != c.warning {
				t.Errorf("logged %q; want the reason, as a warning = %v", l, c.warning)
			}
			if p, ok := Load(dir); !ok || p.Version != "v9.9.9" {
				t.Errorf("Load = %+v, %v", p, ok)
			}
		})
	}
}

// With the site out of use, the release GitHub's API names is held to the
// release key too: its checksums.txt has to carry the key's signature, and has
// to list the archive under the version the release is published as. It was
// taken as it was, so anybody who could keep a copy from the site -- a
// firewall, a DNS answer -- and publish on GitHub could have had anything
// installed.
func TestStageFromGitHubNeedsTheReleaseKeysSignature(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(r *release)
		says   string // what the refusal says, or "" for a release that is installed
	}{
		{"signed by the release key", func(r *release) {}, ""},
		{"without checksums.txt.sig", func(r *release) { r.onGitHub("v9.9.9", r.name, sumsName) }, "carries no checksums.txt.sig"},
		{"with checksums.txt.sig by another key", func(r *release) {
			_, other, _ := ed25519.GenerateKey(nil)
			r.gh.set(ghDownloads+sumsName+sigExt, Sign(other, r.gh.get(ghDownloads+sumsName)))
		}, "not signed by the release key"},
		{"a tampered archive with a checksums.txt to match", func(r *release) {
			_, evil := buildArchive(t, "a program nobody released")
			r.gh.set(ghDownloads+r.name, evil)
			r.gh.set(ghDownloads+sumsName, []byte(fmt.Sprintf("%s  %s\n", sha(evil), r.name)))
		}, "not signed by the release key"},
		{"a signed release published again under a newer tag", func(r *release) {
			r.onGitHub("v9.9.10", r.name, sumsName, sumsName+sigExt)
		}, "not an archive of v9.9.10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := published(t, "the new program")
			logged(t)
			r.dl.breakAll()
			c.break_(r)

			rel, err := Latest(context.Background())
			if err != nil {
				t.Fatalf("Latest: %v", err)
			}
			if rel.Notes != "what changed on GitHub" {
				t.Fatalf("Latest = %+v, want GitHub's release", rel)
			}
			dir := t.TempDir()
			p, err := Stage(context.Background(), rel, dir)
			if c.says == "" {
				if err != nil {
					t.Fatalf("Stage of a signed release from GitHub: %v", err)
				}
				if got := staged(t, p); got != "the new program" {
					t.Errorf("staged %q", got)
				}
				if !r.gh.asked(ghDownloads + sumsName + sigExt) {
					t.Error("checksums.txt.sig was not read from GitHub")
				}
				return
			}
			if err == nil {
				t.Fatalf("Stage installed %q from GitHub", staged(t, p))
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("Stage: %v; want it to say %q", err, c.says)
			}
			if _, ok := Load(dir); ok {
				t.Error("a release refused was recorded as staged")
			}
		})
	}
}

// A candidate is never the latest release, from GitHub any more than from the
// site. GitHub's API never gives a pre-release as its latest, but a candidate
// whose mark was taken off by hand is given like any other, and was taken:
// the site refuses one by its version, and GitHub's path did not look.
func TestLatestFromGitHubRefusesAPreRelease(t *testing.T) {
	cases := []struct {
		name string
		api  map[string]any
	}{
		{"tagged as a candidate", map[string]any{"tag_name": "v9.9.10-rc.1"}},
		{"marked as a pre-release", map[string]any{"tag_name": "v9.9.10", "prerelease": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := published(t, "the new program")
			logged(t)
			r.dl.breakAll()
			api, err := json.Marshal(c.api)
			if err != nil {
				t.Fatal(err)
			}
			r.gh.set("/repos/"+Repo+"/releases/latest", api)

			rel, err := Latest(context.Background())
			if err == nil {
				t.Fatalf("Latest = %s from GitHub, a pre-release", rel.Version)
			}
			if !strings.Contains(err.Error(), "a pre-release, which is never the latest") {
				t.Errorf("Latest: %v; want it refused as a pre-release", err)
			}
		})
	}
}

// latest.json is not signed, and a stale one, or a forged one, can only name
// another signed release. Named an older one, the updater reads that release's
// own signed manifest, finds nothing wrong, and Newer declines to move to it:
// the update is held back, and nothing unsigned or older is ever installed.
func TestAStaleLatestJSONOnlyHoldsAnUpdateBack(t *testing.T) {
	r := published(t, "the new program")
	log := logged(t)
	r.manifest.Version = "v9.9.8"
	r.signAt("v9.9.8")
	r.point("v9.9.8")

	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "v9.9.8" {
		t.Errorf("Latest = %s, want the older release latest.json names", rel.Version)
	}
	if Newer(rel.Version, "v9.9.9") {
		t.Error("an older release is offered as an update to v9.9.9")
	}
	if reqs := r.gh.requests(); len(reqs) > 0 {
		t.Errorf("GitHub was asked for %s", reqs[0].URL.Path)
	}
	if l := log(); l != "" {
		t.Errorf("logged %q for a release that is signed", l)
	}

	// Named a version whose manifest is not signed, it is not believed.
	r.dl.remove("/v9.9.8/manifest.json.sig")
	if rel, err := Latest(context.Background()); err != nil || rel.Notes != "what changed on GitHub" {
		t.Errorf("Latest = %+v, %v; want GitHub's release", rel, err)
	}
}

// A tampered archive is never installed: not from the site, and not from
// GitHub either, which is held to the SHA-256 the signed manifest gave even
// though GitHub's own checksums.txt vouches for what it serves.
func TestStageRefusesATamperedArchiveEverywhere(t *testing.T) {
	r := published(t, "the new program")
	logged(t)
	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, evil := buildArchive(t, "a program nobody released")
	r.dl.set("/v9.9.9/"+r.name, evil)
	r.gh.set(ghDownloads+r.name, evil)
	r.gh.set(ghDownloads+sumsName, []byte(fmt.Sprintf("%s  %s\n", sha(evil), r.name)))

	dir := t.TempDir()
	if _, err := Stage(context.Background(), rel, dir); err == nil {
		t.Fatal("Stage installed an archive that matches no signed checksum")
	}
	if _, ok := Load(dir); ok {
		t.Error("a tampered archive was recorded as staged")
	}
}

// What the updater sends is the request and nothing else: no version, no
// query, no cookie, the same User-Agent every installation sends.
func TestUpdateRequestsCarryNothingIdentifying(t *testing.T) {
	r := published(t, "the new program")
	logged(t)
	r.dl.remove("/v9.9.9/" + r.name) // so GitHub is asked too
	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(context.Background(), rel, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"User-Agent": true, "Accept-Encoding": true, "X-Github-Api-Version": true}
	for _, req := range append(r.dl.requests(), r.gh.requests()...) {
		if req.URL.RawQuery != "" {
			t.Errorf("%s was asked with a query, %q", req.URL.Path, req.URL.RawQuery)
		}
		if ua := req.Header.Get("User-Agent"); ua != "flockdeck-updater" {
			t.Errorf("%s was asked as %q", req.URL.Path, ua)
		}
		for h := range req.Header {
			if !allowed[h] {
				t.Errorf("%s was sent %s", req.URL.Path, h)
			}
		}
	}
}

// A release key that is not the placeholder has to be a key, or every build
// with it would quietly never trust the site.
func TestReleaseKeyIsThePlaceholderOrAKey(t *testing.T) {
	if _, ok := ReleaseKey(); !ok && releaseKey != releaseKeyPlaceholder {
		t.Errorf("releaseKey is %q, which is neither the placeholder nor an Ed25519 public key in PEM or base64", releaseKey)
	}
}

// A release can never ship unable to check what it downloads. The release
// workflow runs the tests with FLOCKDECK_RELEASE_BUILD set, and this one fails
// them while the placeholder is still where the release key belongs; every
// other run of the tests, which has no reason to hold a key, skips it.
func TestReleaseKeyIsInPlace(t *testing.T) {
	if os.Getenv("FLOCKDECK_RELEASE_BUILD") == "" {
		t.Skip("only a release build must carry the release key")
	}
	if _, ok := ReleaseKey(); !ok {
		t.Fatal("internal/selfupdate/releasekey.go still holds the placeholder: put the public key " +
			"`terraform output -raw flockdeck_release_public_key` prints in releaseKey")
	}
}

// The standby key is not required to be a real key yet -- the user generates
// it separately, offline -- but whatever releaseKeyStandby holds has to be
// the placeholder or a real key, never something that silently trusts nothing
// while looking like it might.
func TestStandbyKeyIsThePlaceholderOrAKey(t *testing.T) {
	if _, ok := StandbyKey(); !ok && releaseKeyStandby != releaseKeyStandbyPlaceholder {
		t.Errorf("releaseKeyStandby is %q, which is neither the placeholder nor an Ed25519 public key in PEM or base64", releaseKeyStandby)
	}
}

// If the standby is ever set to a real key, it must not be the same key as
// the primary: one key wearing two hats is exactly the single point of
// failure the standby exists to not be.
func TestStandbyKeyDiffersFromThePrimary(t *testing.T) {
	primary, primaryOK := ReleaseKey()
	standby, standbyOK := StandbyKey()
	if primaryOK && standbyOK && primary.Equal(standby) {
		t.Error("releaseKeyStandby is the same key as releaseKey")
	}
}

// A manifest signed by either trusted key is accepted, one signed by neither
// is refused, and the standby's signature is refused when it is not among the
// keys trusted -- as it is not while releaseKeyStandby is the placeholder.
func TestCheckManifestAcceptsEitherTrustedKeyAndRefusesAThird(t *testing.T) {
	primaryPub, primaryKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	standbyPub, standbyKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{
		Version: "v1.2.3",
		Files: []ManifestFile{
			{Name: sumsName, URL: "https://example.invalid/checksums.txt", SHA256: strings.Repeat("ab", 32), Size: 1},
			{Name: sumsName + sigExt, URL: "https://example.invalid/checksums.txt.sig", SHA256: strings.Repeat("ab", 32), Size: 1},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	keys := []ed25519.PublicKey{primaryPub, standbyPub}

	for _, c := range []struct {
		name string
		key  ed25519.PrivateKey
	}{{"the primary key", primaryKey}, {"the standby key", standbyKey}} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := CheckManifest(keys, data, Sign(c.key, data)); err != nil {
				t.Errorf("CheckManifest signed by %s: %v", c.name, err)
			}
		})
	}
	if _, err := CheckManifest(keys, data, Sign(otherKey, data)); err == nil {
		t.Error("CheckManifest accepted a signature from a third key")
	}
	if _, err := CheckManifest([]ed25519.PublicKey{primaryPub}, data, Sign(standbyKey, data)); err == nil {
		t.Error("CheckManifest accepted the standby's signature while only the primary was trusted")
	}
}

// The site is trusted through the same two keys: a release signed with either
// is taken as the latest, and one signed with neither, or with the standby
// while the build does not yet trust it, is refused.
func TestLatestAcceptsEitherTrustedKey(t *testing.T) {
	primaryPub, primaryKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	standbyPub, standbyKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name         string
		signWith     ed25519.PrivateKey
		trustStandby bool
		ok           bool
	}{
		{"signed by the primary", primaryKey, true, true},
		{"signed by the standby", standbyKey, true, true},
		{"signed by a third key", otherKey, true, false},
		{"signed by the standby while it is not trusted", standbyKey, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := published(t, "the new program")
			oldPrimary, oldStandby := trustedKey, trustedStandbyKey
			trustedKey = primaryPub
			if c.trustStandby {
				trustedStandbyKey = standbyPub
			} else {
				trustedStandbyKey = nil
			}
			t.Cleanup(func() { trustedKey, trustedStandbyKey = oldPrimary, oldStandby })
			r.key = c.signWith
			r.sign()

			_, err := latestFromSite(context.Background())
			if c.ok && err != nil {
				t.Errorf("latestFromSite = %v, want it accepted", err)
			}
			if !c.ok && err == nil {
				t.Error("latestFromSite accepted a signature from an untrusted key")
			}
		})
	}
}

// TrustKeysForTest is exported only so other packages' tests can reach it.
// Outside a test binary -- a plain built program, as anything shipped is --
// it has to panic instead of letting something swap out the keys a build
// trusts.
func TestTrustKeysForTestPanicsOutsideATestBinary(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// Named under the same import path as the real module, or the internal
	// package this calls could not be imported at all: Go's internal/
	// visibility rule is decided by import path text, regardless of which
	// module or directory it is actually built from.
	mod := fmt.Sprintf("module github.com/jmwri/flockdeck/trustkeystest\n\ngo 1.21\n\nrequire github.com/jmwri/flockdeck v0.0.0\n\nreplace github.com/jmwri/flockdeck => %s\n",
		filepath.ToSlash(repoRoot))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	main := "package main\n\nimport \"github.com/jmwri/flockdeck/internal/selfupdate\"\n\nfunc main() {\n\tselfupdate.TrustKeysForTest(nil, nil)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", "-mod=mod", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("TrustKeysForTest did not panic outside a test binary; output:\n%s", out)
	}
	if !strings.Contains(string(out), "TrustKeysForTest is for tests only") {
		t.Errorf("want the panic to say TrustKeysForTest is for tests only, got:\n%s", out)
	}
}

// Signatures are checked against exactly one key and exactly the bytes signed,
// and the signing key's file reads back as the key that wrote it.
func TestSignAndVerify(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseSigningKey(EncodeSigningKey(key))
	if err != nil || !back.Equal(key) {
		t.Fatalf("ParseSigningKey(EncodeSigningKey(k)) = %v, %v", back, err)
	}
	if got := parsePublicKey(EncodePublicKey(pub)); !got.Equal(pub) {
		t.Error("a printed public key does not read back")
	}
	data := []byte("abc  flockdeck_v9.9.9_linux_amd64.tar.gz\n")
	sig := Sign(key, data)
	if strings.Count(string(sig), "\n") != 1 || !strings.HasSuffix(string(sig), "\n") {
		t.Errorf("a .sig is %q, want one line", sig)
	}
	if err := Verify(pub, data, sig); err != nil {
		t.Errorf("Verify of a good signature: %v", err)
	}
	otherPub, _, _ := ed25519.GenerateKey(nil)
	for name, check := range map[string]error{
		"another key":   Verify(otherPub, data, sig),
		"changed bytes": Verify(pub, append([]byte("x"), data...), sig),
		"no signature":  Verify(pub, data, nil),
		"no key":        Verify(nil, data, sig),
	} {
		if check == nil {
			t.Errorf("%s: Verify passed", name)
		}
	}
	if _, err := ParseSigningKey("not a key"); err == nil || strings.Contains(err.Error(), "not a key") {
		t.Errorf("ParseSigningKey of junk = %v; want an error that does not quote the secret", err)
	}
}

// Terraform makes the release key and gives each half as PEM: the private
// half as PKCS#8, the public half as its SubjectPublicKeyInfo. Each reads back
// as the key it came from, pasted as it is: with Windows line endings, or with
// what `terraform output` prints around it. A PEM holding any other kind of
// key, or the other half, is refused, and never quoted back.
func TestKeysReadAsTerraformGivesThem(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	privPEM := pemOf(t, "PRIVATE KEY", must(x509.MarshalPKCS8PrivateKey(key)))
	pubPEM := pemOf(t, "PUBLIC KEY", must(x509.MarshalPKIXPublicKey(pub)))
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	for name, s := range map[string]string{"as given": privPEM, "with Windows line endings": crlf(privPEM)} {
		if got, err := ParseSigningKey(s); err != nil || !got.Equal(key) {
			t.Errorf("ParseSigningKey of Terraform's key %s = %v", name, err)
		}
	}
	for name, s := range map[string]string{
		"as given":                      pubPEM,
		"with Windows line endings":     crlf(pubPEM),
		"as terraform output prints it": "<<EOT\n" + pubPEM + "EOT\n",
	} {
		if got := parsePublicKey(s); !got.Equal(pub) {
			t.Errorf("parsePublicKey of Terraform's public key %s = %v", name, got)
		}
	}

	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecPEM := pemOf(t, "PRIVATE KEY", must(x509.MarshalPKCS8PrivateKey(ec)))
	body := strings.Split(ecPEM, "\n")[1]
	for name, s := range map[string]string{"an ECDSA key": ecPEM, "the public half": pubPEM} {
		if _, err := ParseSigningKey(s); err == nil || strings.Contains(err.Error(), body) || strings.Contains(err.Error(), strings.Split(s, "\n")[1]) {
			t.Errorf("ParseSigningKey of %s = %v; want it refused without quoting it", name, err)
		}
	}
	for name, s := range map[string]string{
		"an ECDSA public key": pemOf(t, "PUBLIC KEY", must(x509.MarshalPKIXPublicKey(&ec.PublicKey))),
		"the private half":    privPEM,
	} {
		if got := parsePublicKey(s); got != nil {
			t.Errorf("parsePublicKey of %s = %v, want nothing", name, got)
		}
	}
}

func pemOf(t *testing.T, kind string, der []byte) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
