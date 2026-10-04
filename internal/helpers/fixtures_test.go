package helpers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
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

// site is a fake GitHub: paths to bodies, with the odd redirect, status and
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
			w.Header().Set("Content-Length", "1000")
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

// releaseSpec describes one release a site serves.
type releaseSpec struct {
	version      string
	goos, goarch string
	members      []member // nil means goodMembers
	archive      []byte   // overrides members when set
	sumOverride  string   // a hash to put in checksums.txt instead of the archive's
	extraLines   string   // appended to checksums.txt
	key          ed25519.PrivateKey
	noSig        bool
	sigOverride  []byte
	latest       bool // also serve at releases/latest/download
	skipArchive  bool
}

func (s *site) release(t *testing.T, e Entry, r releaseSpec) (archive []byte, sums []byte) {
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
	sum := r.sumOverride
	if sum == "" {
		sum = sumHex(archive)
	}
	sums = []byte(fmt.Sprintf("%s  %s\n%s", sum, name, r.extraLines))
	base := "/" + e.Repo + "/releases/download/v" + r.version + "/"
	s.put(base+"checksums.txt", sums)
	if !r.skipArchive {
		s.put(base+name, archive)
	}
	sig := r.sigOverride
	if sig == nil && r.key != nil && !r.noSig {
		sig = selfupdate.Sign(r.key, sums)
	}
	if sig != nil {
		s.put(base+"checksums.txt.sig", sig)
	}
	if r.latest {
		latest := "/" + e.Repo + "/releases/latest/download/"
		s.put(latest+"checksums.txt", sums)
		if sig != nil {
			s.put(latest+"checksums.txt.sig", sig)
		}
	}
	return archive, sums
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

// fixture is a store in a temp dir, a fake site and an installer between them.
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
	f.rebuild(mutate...)
	return f
}

func (f *fixture) rebuild(mutate ...func(*Options)) {
	o := Options{
		Store: f.store, Base: f.site.URL, AllowURL: loopbackOnly, Stall: 5 * time.Second,
		GOOS: f.goos, GOARCH: f.arch, Lookup: lookupTest(f.entry),
	}
	for _, m := range mutate {
		m(&o)
	}
	f.in = NewInstaller(o)
}

// publish serves a signed release at its own tag and as the latest.
func (f *fixture) publish(version string) []byte {
	f.t.Helper()
	a, _ := f.site.release(f.t, f.entry, releaseSpec{version: version, goos: f.goos, goarch: f.arch, key: f.key, latest: true})
	return a
}

func (f *fixture) install(version string) (*InstallInfo, error) {
	f.t.Helper()
	return f.in.Install(f.t.Context(), f.entry.ID, version, false)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
