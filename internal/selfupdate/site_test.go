package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
// the site, signed, and on GitHub, unsigned.
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

	oldKey, oldSite, oldAPI, oldGH := trustedKey, siteURL, githubAPIURL, githubURL
	trustedKey, siteURL, githubAPIURL, githubURL = pub, r.dl.srv.URL, r.gh.srv.URL, r.gh.srv.URL
	t.Cleanup(func() { trustedKey, siteURL, githubAPIURL, githubURL = oldKey, oldSite, oldAPI, oldGH })

	sums := []byte(fmt.Sprintf("%s  %s\n", sha(r.archive), r.name))
	sumsSig := Sign(key, sums)
	for name, data := range map[string][]byte{r.name: r.archive, sumsName: sums, sumsName + sigExt: sumsSig} {
		r.dl.set("/v9.9.9/"+name, data)
		r.manifest.Files = append(r.manifest.Files, ManifestFile{
			Name: name, URL: r.dl.srv.URL + "/v9.9.9/" + name, SHA256: sha(data), Size: int64(len(data)),
		})
		if name != sumsName+sigExt {
			r.gh.set(ghDownloads+name, data)
		}
	}
	r.manifest.Version = "v9.9.9"
	r.manifest.Date = time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	r.manifest.NotesURL = "https://github.com/" + Repo + "/releases/tag/v9.9.9"
	r.manifest.Notes = "what changed"
	r.sign()

	api, _ := json.Marshal(map[string]any{
		"tag_name": "v9.9.9", "body": "what changed on GitHub", "html_url": r.manifest.NotesURL,
		"assets": []map[string]any{
			{"name": r.name, "browser_download_url": r.gh.srv.URL + ghDownloads + r.name},
			{"name": sumsName, "browser_download_url": r.gh.srv.URL + ghDownloads + sumsName},
		},
	})
	r.gh.set("/repos/"+Repo+"/releases/latest", api)
	return r
}

// sign puts the manifest as it stands on the site, with its signature.
func (r *release) sign() {
	data, err := json.MarshalIndent(r.manifest, "", "  ")
	if err != nil {
		panic(err) // a struct of strings, numbers and a time always marshals
	}
	r.dl.set("/latest.json", data)
	r.dl.set("/latest.json.sig", Sign(r.key, data))
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

// The site is asked first, its manifest is believed once its signature
// checks, and the archive comes from it too: GitHub is not asked anything, and
// nothing is logged.
func TestLatestAndStageUseTheSignedSite(t *testing.T) {
	r := published(t, "the new program")
	log := logged(t)

	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Version != "v9.9.9" || rel.Notes != "what changed" || rel.URL != r.manifest.NotesURL {
		t.Errorf("Latest = %s %q %s; want the release latest.json describes", rel.Version, rel.Notes, rel.URL)
	}
	if got := rel.DownloadSize(); got != int64(len(r.archive)) {
		t.Errorf("DownloadSize = %d, want the size latest.json gives, %d", got, len(r.archive))
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
// does not check is a warning: the site served something that was not
// released.
func TestLatestFallsBackToGitHub(t *testing.T) {
	cases := []struct {
		name    string
		break_  func(r *release)
		says    string
		warning bool
	}{
		{"out of reach", func(r *release) { r.dl.breakAll() }, "could not read the latest release", false},
		{"an error", func(r *release) { r.dl.fail("/latest.json", http.StatusServiceUnavailable) }, "503", false},
		{"no signature", func(r *release) { r.dl.remove("/latest.json.sig") }, "404", false},
		{"a page that is not the manifest", func(r *release) {
			r.dl.set("/latest.json", []byte("<html>maintenance</html>"))
		}, "not signed by the release key", true},
		{"a manifest changed after signing", func(r *release) {
			r.dl.set("/latest.json", []byte(strings.Replace(string(r.dl.get("/latest.json")), "v9.9.9", "v9.9.8", 1)))
		}, "not signed by the release key", true},
		{"signed by another key", func(r *release) {
			_, other, _ := ed25519.GenerateKey(nil)
			r.dl.set("/latest.json.sig", Sign(other, r.dl.get("/latest.json")))
		}, "not signed by the release key", true},
		{"a signed manifest naming no version", func(r *release) {
			r.manifest.Version = "latest"
			r.sign()
		}, "not a version", false},
		{"a signed manifest without checksums.txt.sig", func(r *release) {
			r.manifest.Files = r.manifest.Files[:0]
			r.sign()
		}, "does not list", false},
		{"no release key in this build", func(r *release) { trustedKey = nil }, "no release key", false},
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
		t.Errorf("releaseKey is %q, which is neither the placeholder nor a key `cmd/release -keygen` printed", releaseKey)
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
		t.Fatal("internal/selfupdate/releasekey.go still holds the placeholder: generate the key with " +
			"`go run ./cmd/release -keygen <file>` and put the public key it prints in releaseKey")
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
