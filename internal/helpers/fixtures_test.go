package helpers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// testEntry is a helper with small limits, for tests that need to hit them.
func testEntry() Entry {
	e := lens
	e.MinVersion = "0.2.0"
	e.MaxArchive = 1 << 20
	e.MaxUnpacked = 1 << 20
	e.MaxFiles = 50
	return e
}

func lookupTest(e Entry) func(string) (Entry, bool) {
	return func(id string) (Entry, bool) {
		if id == e.ID {
			return e, true
		}
		return Entry{}, false
	}
}

// loopbackOnly allows plain http to 127.0.0.1, which is all a test server is.
func loopbackOnly(u *url.URL) bool { return u.Scheme == "http" && u.Hostname() == "127.0.0.1" }

// site is a fake CDN: paths to bodies, with the odd redirect, status and
// hang for the tests that need one.
type site struct {
	*httptest.Server
	mu        sync.Mutex
	files     map[string][]byte
	redirects map[string]string
	status    map[string]int
	hang      map[string]bool
	bigLength map[string]int64
	hits      map[string]int
	chunked   map[string]bool
}

func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{
		files: map[string][]byte{}, redirects: map[string]string{}, status: map[string]int{},
		hang: map[string]bool{}, bigLength: map[string]int64{}, hits: map[string]int{}, chunked: map[string]bool{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		body, ok := s.files[r.URL.Path]
		to, redirect := s.redirects[r.URL.Path]
		code := s.status[r.URL.Path]
		hang := s.hang[r.URL.Path]
		big := s.bigLength[r.URL.Path]
		chunked := s.chunked[r.URL.Path]
		s.mu.Unlock()
		switch {
		case redirect:
			http.Redirect(w, r, to, http.StatusFound)
		case code != 0:
			http.Error(w, "status", code)
		case hang:
			w.Header().Set("Content-Length", fmt.Sprint(max(len(body), 1)))
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case big > 0:
			w.Header().Set("Content-Length", fmt.Sprint(big))
			w.WriteHeader(http.StatusOK)
			chunk := make([]byte, 64<<10)
			for sent := int64(0); sent < big; sent += int64(len(chunk)) {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		case !ok:
			http.NotFound(w, r)
		case chunked:
			// Flushing first makes the response chunked, with no Content-Length.
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = w.Write(body)
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *site) put(path string, body []byte) {
	s.mu.Lock()
	s.files[path] = body
	s.mu.Unlock()
}

func (s *site) remove(path string) {
	s.mu.Lock()
	delete(s.files, path)
	s.mu.Unlock()
}

func (s *site) hitCount(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// member is one entry of a test archive.
type member struct {
	name string
	body string
	typ  byte   // tar type flag; 0 means a regular file
	link string // link target
	mode int64
	// zip only: the file mode bits to record
	zipMode os.FileMode
}

func tarGz(t *testing.T, members []member) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		typ := m.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := m.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: m.name, Typeflag: typ, Mode: mode, Linkname: m.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(m.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(m.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipBytes(t *testing.T, members []member) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		h := &zip.FileHeader{Name: m.name, Method: zip.Deflate}
		switch {
		case m.zipMode != 0:
			h.SetMode(m.zipMode)
		case m.typ == tar.TypeDir:
			h.SetMode(os.ModeDir | 0o755)
		default:
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(m.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// archiveFor builds the archive for goos in the format that platform uses.
func archiveFor(t *testing.T, goos string, members []member) []byte {
	t.Helper()
	if goos == "windows" {
		return zipBytes(t, members)
	}
	return tarGz(t, members)
}

// goodMembers is a release's archive: the folder, the binary, the README.
func goodMembers(e Entry, version, goos, goarch string) []member {
	top := e.TopFolder(version, goos, goarch)
	return []member{
		{name: top + "/", typ: tar.TypeDir},
		{name: top + "/" + e.BinaryName(goos), body: "not a real program", mode: 0o755},
		{name: top + "/README.md", body: "hello"},
	}
}

func sumHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// releaseSpec describes one release a fake CDN serves, laid out as the lens
// publish script lays it out: /lens/latest.json, and under /lens/<tag>/ the
// archive, checksums.txt(.sig) and manifest.json(.sig).
type releaseSpec struct {
	version      string // without the "v"
	goos, goarch string
	members      []member // nil means goodMembers
	archive      []byte   // overrides members when set
	// sumOverride is the hash put in the manifest AND checksums.txt in place of
	// the archive's (a download that does not match what was signed); sumsHash
	// is for checksums.txt alone (the two files disagree).
	sumOverride, sumsHash string
	extraLines            string // appended to checksums.txt
	key                   ed25519.PrivateKey
	noSig                 bool   // no manifest.json.sig at all
	noSumsSig             bool   // no checksums.txt.sig
	sigOverride           []byte // the manifest's signature, as served
	sumsSigOverride       []byte
	date                  time.Time // zero means an hour ago
	latest                bool      // serve latest.json naming this version
	pointerTo             string    // latest.json names this tag instead (it lies)
	mutate                func(*selfupdate.Manifest)
	manifestBytes         []byte // served (and signed) instead of the manifest built
	size                  int64  // the size put in the manifest, when not 0
	url                   string // the URL put in the manifest, when not ""
	extraFiles            []selfupdate.ManifestFile
	skipArchive           bool
}

// cdnBase is where a release's files are, below the site's /lens.
func cdnBase(version string) string { return "/lens/v" + version + "/" }

func (s *site) release(t *testing.T, e Entry, r releaseSpec) (archive []byte, manifest []byte) {
	t.Helper()
	if r.goos == "" {
		r.goos = "linux"
	}
	if r.goarch == "" {
		r.goarch = "amd64"
	}
	members := r.members
	if members == nil {
		members = goodMembers(e, r.version, r.goos, r.goarch)
	}
	archive = r.archive
	if archive == nil {
		archive = archiveFor(t, r.goos, members)
	}
	name := e.ArchiveName(r.version, r.goos, r.goarch)
	tag := "v" + r.version
	base := cdnBase(r.version)
	hash := sumHex(archive)
	if r.sumOverride != "" {
		hash = r.sumOverride
	}
	sumsHash := hash
	if r.sumsHash != "" {
		sumsHash = r.sumsHash
	}
	sums := []byte(fmt.Sprintf("%s  %s\n%s", sumsHash, name, r.extraLines))
	var sumsSig []byte
	switch {
	case r.sumsSigOverride != nil:
		sumsSig = r.sumsSigOverride
	case r.key != nil && !r.noSumsSig:
		sumsSig = selfupdate.Sign(r.key, sums)
	}

	size := int64(len(archive))
	if r.size != 0 {
		size = r.size
	}
	fileURL := s.URL + base + name
	if r.url != "" {
		fileURL = r.url
	}
	date := r.date
	if date.IsZero() {
		date = time.Now().Add(-time.Hour)
	}
	m := selfupdate.Manifest{
		Version: tag, Date: date, NotesURL: "https://example.invalid/notes",
		Files: []selfupdate.ManifestFile{
			{Name: name, URL: fileURL, SHA256: hash, Size: size},
			{Name: "checksums.txt", URL: s.URL + base + "checksums.txt", SHA256: sumHex(sums), Size: int64(len(sums))},
			{Name: "checksums.txt.sig", URL: s.URL + base + "checksums.txt.sig", SHA256: sumHex(sumsSig), Size: int64(len(sumsSig))},
		},
	}
	m.Files = append(m.Files, r.extraFiles...)
	if r.mutate != nil {
		r.mutate(&m)
	}
	manifest = r.manifestBytes
	if manifest == nil {
		var err error
		if manifest, err = json.Marshal(m); err != nil {
			t.Fatal(err)
		}
	}
	var sig []byte
	switch {
	case r.sigOverride != nil:
		sig = r.sigOverride
	case r.key != nil && !r.noSig:
		sig = selfupdate.Sign(r.key, manifest)
	}

	s.put(base+"manifest.json", manifest)
	s.put(base+"checksums.txt", sums)
	if !r.skipArchive {
		s.put(base+name, archive)
	}
	for path, content := range map[string][]byte{base + "manifest.json.sig": sig, base + "checksums.txt.sig": sumsSig} {
		if content != nil {
			s.put(path, content)
		} else {
			s.remove(path)
		}
	}
	if r.latest || r.pointerTo != "" {
		to := tag
		if r.pointerTo != "" {
			to = r.pointerTo
		}
		s.put("/lens/latest.json", []byte(`{"version":"`+to+`"}`))
	}
	return archive, manifest
}

// keys generates a signing key and trusts its public half for the test.
func trustNewKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(selfupdate.TrustKeysForTest(pub, nil))
	return priv
}

// fixture is a store in a temp dir, a fake CDN and an installer between them.
type fixture struct {
	t     *testing.T
	store *Store
	site  *site
	entry Entry
	in    *Installer
	key   ed25519.PrivateKey
	goos  string
	arch  string
}

func newFixture(t *testing.T, mutate ...func(*Options)) *fixture {
	t.Helper()
	f := &fixture{t: t, entry: testEntry(), goos: "linux", arch: "amd64"}
	f.key = trustNewKey(t)
	f.store = &Store{Root: t.TempDir()}
	f.site = newSite(t)
	f.entry.Source = f.site.URL + "/lens"
	f.rebuild(mutate...)
	return f
}

func (f *fixture) rebuild(mutate ...func(*Options)) {
	o := Options{
		Store: f.store, AllowURL: loopbackOnly, Stall: 5 * time.Second,
		GOOS: f.goos, GOARCH: f.arch, Lookup: lookupTest(f.entry),
	}
	for _, m := range mutate {
		m(&o)
	}
	f.in = NewInstaller(o)
}

// archivePath is where a release's archive is served, for tests that tamper
// with how it is served.
func (f *fixture) archivePath(version string) string {
	return cdnBase(version) + f.entry.ArchiveName(version, f.goos, f.arch)
}

// publish serves a signed release at its own tag and as the latest.
func (f *fixture) publish(version string) []byte {
	f.t.Helper()
	a, _ := f.site.release(f.t, f.entry, releaseSpec{version: version, goos: f.goos, goarch: f.arch, key: f.key, latest: true})
	return a
}

func (f *fixture) install(version string) (*InstallInfo, error) {
	f.t.Helper()
	return f.in.Install(f.t.Context(), f.entry.ID, version)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
