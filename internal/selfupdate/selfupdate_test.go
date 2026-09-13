package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runningOld makes this test binary stand in for a program still running: set,
// it sleeps rather than running the tests.
const runningOld = "SELFUPDATE_TEST_RUNNING_OLD"

func TestMain(m *testing.M) {
	if os.Getenv(runningOld) != "" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A second update put in place before a restart finds the first one's .old
// still running — the instance started before either update is running from
// it — and Windows will neither delete that file nor rename anything over it.
// The update has to go in all the same, not send the user off to an
// administrator shell.
func TestApplyWhileTheLastReplacedProgramIsStillRunning(t *testing.T) {
	dir, install := t.TempDir(), t.TempDir()
	exe := filepath.Join(install, binaryName)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFile(self, exe+".old"); err != nil {
		t.Fatal(err)
	}
	old := exec.Command(exe + ".old")
	old.Env = append(os.Environ(), runningOld+"=1")
	if err := old.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { old.Process.Kill(); old.Wait() })

	if err := os.WriteFile(exe, []byte("the version the first update put in"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staging", binaryName)
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("the new program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := save(dir, &Pending{Version: "v9.9.9", Binary: staged}); err != nil {
		t.Fatal(err)
	}

	if err := Apply(dir, exe); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "the new program" {
		t.Errorf("installed binary = %q, want the new program", got)
	}
}

// buildArchive makes a release archive of the shape cmd/release produces, in
// the format this platform's release uses, holding body as the binary.
func buildArchive(t *testing.T, body string) (name string, data []byte) {
	t.Helper()
	return buildArchiveOf(t, map[string]string{binaryName: body})
}

// buildArchiveOf is buildArchive holding each named file with its body.
func buildArchiveOf(t *testing.T, files map[string]string) (name string, data []byte) {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	if runtime.GOOS == "windows" {
		bw := &byteWriter{}
		zw := zip.NewWriter(bw)
		for _, n := range names {
			h := &zip.FileHeader{Name: n, Method: zip.Deflate}
			h.SetMode(0o755)
			w, err := zw.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(files[n])); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return "flockdeck_v9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip", bw.b
	}

	bw := &byteWriter{}
	gz := gzip.NewWriter(bw)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{
			Name: n, Mode: 0o755, Size: int64(len(files[n])), Format: tar.FormatPAX,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return "flockdeck_v9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz", bw.b
}

type byteWriter struct{ b []byte }

func (w *byteWriter) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }

// testKey signs the checksums.txt of the releases these tests serve as
// GitHub, which carries the release key's signature of every release's.
var testKey = func() ed25519.PrivateKey {
	_, k, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	return k
}()

// signedByTestKey is checksums.txt.sig for sums, signed by testKey, which the
// updater trusts in place of the release key until the test ends.
func signedByTestKey(t *testing.T, sums []byte) []byte {
	t.Helper()
	old := trustedKey
	trustedKey = testKey.Public().(ed25519.PublicKey)
	t.Cleanup(func() { trustedKey = old })
	return Sign(testKey, sums)
}

// releaseServer stands in for GitHub, serving one release, its checksums.txt
// signed as the release workflow signs it, whose checksums file can be made to
// disagree with the archive so the checking path can be tested.
func releaseServer(t *testing.T, archiveName string, archive []byte, sum string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	sums := []byte(fmt.Sprintf("%s  %s\n", sum, archiveName))
	sig := signedByTestKey(t, sums)
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write(sums) })
	mux.HandleFunc("/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{
			Version: "v9.9.9",
			Notes:   "notes",
			URL:     "https://example.invalid/rel",
			Assets: []Asset{
				{Name: archiveName, URL: srv.URL + "/archive"},
				{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"},
				{Name: "checksums.txt.sig", URL: srv.URL + "/checksums.txt.sig"},
			},
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func fetchRelease(t *testing.T, url string) *Release {
	t.Helper()
	resp, err := get(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		t.Fatal(err)
	}
	return &rel
}

func TestStageUnpacksTheBinaryAndRecordsIt(t *testing.T) {
	const body = "the new program"
	name, archive := buildArchive(t, body)
	h := sha256.Sum256(archive)
	srv := releaseServer(t, name, archive, hex.EncodeToString(h[:]))

	dir := t.TempDir()
	p, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if p.Version != "v9.9.9" {
		t.Errorf("version = %q, want v9.9.9", p.Version)
	}
	got, err := os.ReadFile(p.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("staged binary = %q, want %q", got, body)
	}

	loaded, ok := Load(dir)
	if !ok || loaded.Version != "v9.9.9" {
		t.Errorf("Load = %+v, %v; want the staged update", loaded, ok)
	}
}

// Two downloads can be under way at once -- `flockdeck update` while the
// application's own check is downloading -- and each began by clearing the
// one work directory there was, the other's half-written archive with it.
func TestTwoStagingsAtOnceBothFinish(t *testing.T) {
	const body = "the new program"
	name, archive := buildArchive(t, body)
	h := sha256.Sum256(archive)

	// The first download stays under way, its file open and half written,
	// until the second has finished.
	started, hold := make(chan struct{}), make(chan struct{})
	var downloads atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) {
		if downloads.Add(1) > 1 {
			w.Write(archive)
			return
		}
		w.Write(archive[:len(archive)/2])
		w.(http.Flusher).Flush()
		close(started)
		<-hold
		w.Write(archive[len(archive)/2:])
	})
	sums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(h[:]), name))
	sig := signedByTestKey(t, sums)
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write(sums) })
	mux.HandleFunc("/checksums.txt.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Version: "v9.9.9", Assets: []Asset{
			{Name: name, URL: srv.URL + "/archive"},
			{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"},
			{Name: "checksums.txt.sig", URL: srv.URL + "/checksums.txt.sig"},
		}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	letGo := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(letGo) // before the server closes, which waits for the handler

	dir := t.TempDir()
	first := make(chan error, 1)
	go func() {
		_, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir)
		first <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first download never started")
	}
	if _, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir); err != nil {
		t.Errorf("staging while another download was under way: %v", err)
	}
	letGo()
	if err := <-first; err != nil {
		t.Errorf("the download that was under way, once the other had finished: %v", err)
	}
	p, ok := Load(dir)
	if !ok {
		t.Fatal("nothing is staged")
	}
	if got, err := os.ReadFile(p.Binary); err != nil || string(got) != body {
		t.Errorf("staged binary = %q, %v; want %q", got, err, body)
	}
}

// What an interrupted download left is cleared once it is old enough that
// nothing can still be writing it, and not before: a young one may be another
// download's, under way.
func TestStageClearsWhatAnInterruptedDownloadLeft(t *testing.T) {
	name, archive := buildArchive(t, "the new program")
	h := sha256.Sum256(archive)
	srv := releaseServer(t, name, archive, hex.EncodeToString(h[:]))

	dir := t.TempDir()
	old, young := filepath.Join(dir, "staging.new"), filepath.Join(dir, "staging.new-123")
	for _, d := range []string{old, young} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-2 * abandonedWork)
	if err := os.Chtimes(old, long, long); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the work directory of a download interrupted long ago is still there")
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("a work directory another download may be writing was cleared: %v", err)
	}
}

// Apply moves aside the file a link leads to, and the sweep looked beside the
// link: started through one, as macOS reports a program, the old program was
// never cleared away.
func TestSweepFindsWhatApplyMovedAsideThroughALink(t *testing.T) {
	dir := t.TempDir()
	realDir, linkDir := filepath.Join(dir, "real"), filepath.Join(dir, "bin")
	for _, d := range []string{realDir, linkDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	program := filepath.Join(realDir, binaryName)
	if err := os.WriteFile(program, []byte("the program"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, binaryName)
	if err := os.Symlink(program, link); err != nil {
		t.Skipf("links cannot be made here: %v", err)
	}
	if err := os.WriteFile(program+".old", []byte("the old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	Sweep(link)
	if _, err := os.Stat(program + ".old"); err == nil {
		t.Error("the program an update moved aside is still there, beside the file the link leads to")
	}
	if _, err := os.Stat(program); err != nil {
		t.Errorf("the program itself went: %v", err)
	}
}

// A download that keeps arriving is not cut off for being slow, and one that
// stops arriving is given up on. The client's deadline covered reading the
// whole body, so below about 31KB/s the Windows archive could never finish.
func TestASlowDownloadIsKeptAndAStalledOneIsNot(t *testing.T) {
	old := stallTimeout
	// A whole second: a Windows runner under load has been seen to go 300ms
	// between two of the bytes below, and give up on the download that was
	// still arriving.
	stallTimeout = time.Second
	t.Cleanup(func() { stallTimeout = old })

	// A byte every 25ms: a second and a half in all, longer than may pass with
	// nothing arriving, and never anywhere near that long without a byte.
	body := bytes.Repeat([]byte("x"), 60)
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := range body {
			if r.URL.Path == "/stalls" && i == len(body)/2 {
				select {
				case <-hold:
				case <-r.Context().Done():
				}
				return
			}
			w.Write(body[i : i+1])
			w.(http.Flusher).Flush()
			time.Sleep(25 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(sync.OnceFunc(func() { close(hold) }))

	h := sha256.Sum256(body)
	want := hex.EncodeToString(h[:])
	if err := download(context.Background(), srv.URL+"/slow", filepath.Join(t.TempDir(), "slow"), want, int64(len(body))); err != nil {
		t.Errorf("a download that kept arriving, slowly: %v", err)
	}
	start := time.Now()
	err := download(context.Background(), srv.URL+"/stalls", filepath.Join(t.TempDir(), "stalls"), want, int64(len(body)))
	if err == nil || !strings.Contains(err.Error(), "stopped sending") {
		t.Errorf("a download that stopped arriving = %v, want it given up on as having stopped", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("giving up on a stalled download took %s", d)
	}
}

// A download that goes on past the size its release gives is given up on as
// soon as it does. The size is signed in the site's manifest, and a body that
// never ended used to be written to disk until requestLimit, an hour, ran out,
// on every check, since the hash can only be checked at the end.
func TestADownloadLargerThanItsSizeIsCutOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	// Long enough for a download that is cut off, and far short of what an
	// endless one read to the end would take.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dest := filepath.Join(t.TempDir(), "archive")
	err := download(ctx, srv.URL, dest, sha([]byte("the release")), 1<<20)
	var mismatch mismatchError
	if !errors.As(err, &mismatch) || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("download of a body that never ends = %v, want it refused as larger than its size", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("what was downloaded is still on disk: %v", err)
	}
}

func TestStageRefusesAnArchiveThatDoesNotMatchItsChecksum(t *testing.T) {
	name, archive := buildArchive(t, "the new program")
	// A hash of something else entirely: what a corrupted or swapped download
	// looks like from here.
	wrong := sha256.Sum256([]byte("not what was served"))
	srv := releaseServer(t, name, archive, hex.EncodeToString(wrong[:]))

	dir := t.TempDir()
	if _, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir); err == nil {
		t.Fatal("Stage accepted an archive that did not match its published checksum")
	}
	if _, ok := Load(dir); ok {
		t.Error("a rejected download was still recorded as staged")
	}
}

// A newer release that fails to download must not cost the update already
// staged: the top bar is still offering it, and a restart has to apply it.
func TestStageKeepsTheStagedUpdateWhenANewerOneFails(t *testing.T) {
	name, archive := buildArchive(t, "the new program")
	h := sha256.Sum256(archive)
	good := releaseServer(t, name, archive, hex.EncodeToString(h[:]))
	dir := t.TempDir()
	if _, err := Stage(context.Background(), fetchRelease(t, good.URL+"/release"), dir); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	wrong := sha256.Sum256([]byte("not what was served"))
	bad := releaseServer(t, name, archive, hex.EncodeToString(wrong[:]))
	newer := fetchRelease(t, bad.URL+"/release")
	newer.Version = "v9.9.10"
	if _, err := Stage(context.Background(), newer, dir); err == nil {
		t.Fatal("Stage accepted an archive that did not match its published checksum")
	}

	p, ok := Load(dir)
	if !ok || p.Version != "v9.9.9" {
		t.Fatalf("Load = %+v, %v; want v9.9.9 still staged", p, ok)
	}
	if got, err := os.ReadFile(p.Binary); err != nil || string(got) != "the new program" {
		t.Errorf("staged binary = %q, %v; want it untouched", got, err)
	}
}

// A download that cannot be put where the staged update is must not cost that
// update. The staged one was removed first, so a rename that then failed -- on
// Windows, a virus scanner holding the new program -- left nothing to apply
// while the top bar went on offering it.
func TestStageKeepsTheStagedUpdateWhenItsReplacementCannotBePutInPlace(t *testing.T) {
	name, archive := buildArchive(t, "the new program")
	h := sha256.Sum256(archive)
	good := releaseServer(t, name, archive, hex.EncodeToString(h[:]))
	dir := t.TempDir()
	if _, err := Stage(context.Background(), fetchRelease(t, good.URL+"/release"), dir); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	staging := filepath.Join(dir, "staging")
	old := rename
	rename = func(src, dst string) error {
		if dst == staging && strings.HasPrefix(filepath.Base(src), "staging.new-") {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: errors.New("held by a virus scanner")}
		}
		return old(src, dst)
	}
	t.Cleanup(func() { rename = old })

	name, archive = buildArchive(t, "another program")
	h = sha256.Sum256(archive)
	other := releaseServer(t, name, archive, hex.EncodeToString(h[:]))
	if _, err := Stage(context.Background(), fetchRelease(t, other.URL+"/release"), dir); err == nil {
		t.Fatal("Stage reported a download it could not put in place as staged")
	}

	p, ok := Load(dir)
	if !ok {
		t.Fatal("the update staged before is gone")
	}
	if got, err := os.ReadFile(p.Binary); err != nil || string(got) != "the new program" {
		t.Errorf("staged binary = %q, %v; want the update staged before", got, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "staging.old-") {
			t.Errorf("%s was left behind with the update put back", e.Name())
		}
	}
}

// Replacing what is staged waits out a moment's hold on it, as the state
// files' saves do. Windows will not rename a directory while anything has a
// file in it open, and a virus scanner opens every program written.
func TestStageWaitsOutAMomentsHoldOnTheStagedUpdate(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to rename a directory holding a file that is open")
	}
	name, archive := buildArchive(t, "the new program")
	h := sha256.Sum256(archive)
	srv := releaseServer(t, name, archive, hex.EncodeToString(h[:]))
	dir := t.TempDir()
	first, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	// The staged program is opened just before the swap, as a scanner would
	// open it, and let go of a tenth of a second later.
	old := rename
	var hold sync.Once
	rename = func(src, dst string) error {
		hold.Do(func() {
			f, err := os.Open(first.Binary)
			if err != nil {
				t.Errorf("hold the staged program: %v", err)
				return
			}
			time.AfterFunc(100*time.Millisecond, func() { f.Close() })
		})
		return old(src, dst)
	}
	t.Cleanup(func() { rename = old })
	rel := fetchRelease(t, srv.URL+"/release")

	if _, err := Stage(context.Background(), rel, dir); err != nil {
		t.Fatalf("Stage with the staged program held open for a moment: %v", err)
	}
	if p, ok := Load(dir); !ok || staged(t, p) != "the new program" {
		t.Errorf("Load = %+v, %v; want the new download staged", p, ok)
	}
}

// If the record of what is staged cannot be written, the staging directory
// already holds the new release; the old record must not survive to name it,
// or the next exit installs one release under another's version. On Windows a
// record another process is reading cannot be replaced, which is how it fails.
func TestStageNeverLeavesARecordForAnotherRelease(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to replace a file another handle has open")
	}
	name, archive := buildArchive(t, "the new program")
	h := sha256.Sum256(archive)
	srv := releaseServer(t, name, archive, hex.EncodeToString(h[:]))
	dir := t.TempDir()
	first, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// The record names another release than the one staged next.
	if err := save(dir, &Pending{Version: "v9.9.8", Binary: first.Binary}); err != nil {
		t.Fatal(err)
	}

	held, err := os.Open(pendingPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir); err == nil {
		t.Fatal("Stage replaced a record another handle held open")
	}
	if p, ok := Load(dir); ok {
		t.Errorf("Load = %s staged, over a staging directory that now holds another release", p.Version)
	}
}

// A checksums file whose entry is not a SHA-256 at all has to be refused, not
// compared. Comparing it panicked, in the background watcher, which ends the
// whole application.
func TestStageRefusesAChecksumThatIsNotASHA256(t *testing.T) {
	name, archive := buildArchive(t, "the new program")
	srv := releaseServer(t, name, archive, "abc123")

	dir := t.TempDir()
	if _, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir); err == nil {
		t.Fatal("Stage accepted a checksum that is not a SHA-256")
	}
	if _, ok := Load(dir); ok {
		t.Error("a download with no usable checksum was recorded as staged")
	}
}

func TestStageRefusesAReleaseWithNothingForThisPlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{
			Version: "v9.9.9",
			Assets:  []Asset{{Name: "flockdeck_v9.9.9_plan9_mips.tar.gz"}, {Name: "checksums.txt"}},
		})
	}))
	defer srv.Close()

	if _, err := Stage(context.Background(), fetchRelease(t, srv.URL), t.TempDir()); err != ErrNoAsset {
		t.Errorf("err = %v, want ErrNoAsset", err)
	}
}

func TestApplySwapsTheBinaryAndMovesTheOldOneAside(t *testing.T) {
	dir := t.TempDir()
	install := t.TempDir()

	exe := filepath.Join(install, binaryName)
	if err := os.WriteFile(exe, []byte("the old program"), 0o755); err != nil {
		t.Fatal(err)
	}

	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(staging, binaryName)
	if err := os.WriteFile(staged, []byte("the new program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := save(dir, &Pending{Version: "v9.9.9", Binary: staged}); err != nil {
		t.Fatal(err)
	}

	if err := Apply(dir, exe); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the new program" {
		t.Errorf("installed binary = %q, want the new program", got)
	}
	if _, err := os.Stat(exe + ".old"); err != nil {
		t.Error("the replaced program was not moved aside for the next start to sweep")
	}
	if _, ok := Load(dir); ok {
		t.Error("the update was still recorded as pending after being applied")
	}

	Sweep(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Error("Sweep left the moved-aside program behind")
	}
}

// The Windows release carries the console twin an API agent's pane runs, and
// it is staged with the program. A release from before the twin stages the
// program alone.
func TestStageUnpacksTheChatTwin(t *testing.T) {
	if chatName == "" {
		t.Skip("only the Windows release has a console twin")
	}
	for _, withTwin := range []bool{true, false} {
		files := map[string]string{binaryName: "the new program"}
		if withTwin {
			files[chatName] = "the new chat client"
		}
		name, archive := buildArchiveOf(t, files)
		h := sha256.Sum256(archive)
		srv := releaseServer(t, name, archive, hex.EncodeToString(h[:]))

		dir := t.TempDir()
		p, err := Stage(context.Background(), fetchRelease(t, srv.URL+"/release"), dir)
		if err != nil {
			t.Fatalf("with the twin %v: Stage: %v", withTwin, err)
		}
		if !withTwin {
			if p.Chat != "" {
				t.Errorf("a release without the twin staged one: %q", p.Chat)
			}
			continue
		}
		if got, err := os.ReadFile(p.Chat); err != nil || string(got) != "the new chat client" {
			t.Errorf("staged twin = %q, %v; want the new chat client", got, err)
		}
		if loaded, ok := Load(dir); !ok || loaded.Chat != p.Chat {
			t.Errorf("Load = %+v, %v; want the staged twin recorded", loaded, ok)
		}
	}
}

// The twin goes in with the program, whether the installation had one yet or
// not, and what it replaced is swept like the program's.
func TestApplyPutsTheChatTwinInPlaceToo(t *testing.T) {
	if chatName == "" {
		t.Skip("only the Windows release has a console twin")
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, hadTwin := range []bool{true, false} {
		dir, install := t.TempDir(), t.TempDir()
		exe, twin := filepath.Join(install, binaryName), filepath.Join(install, chatName)
		write(exe, "the old program")
		if hadTwin {
			write(twin, "the old chat client")
		}
		staging := filepath.Join(dir, "staging")
		if err := os.MkdirAll(staging, 0o755); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(staging, binaryName), "the new program")
		write(filepath.Join(staging, chatName), "the new chat client")
		if err := save(dir, &Pending{Version: "v9.9.9", Binary: filepath.Join(staging, binaryName), Chat: filepath.Join(staging, chatName)}); err != nil {
			t.Fatal(err)
		}

		if err := Apply(dir, exe); err != nil {
			t.Fatalf("had a twin %v: Apply: %v", hadTwin, err)
		}
		if got, _ := os.ReadFile(exe); string(got) != "the new program" {
			t.Errorf("had a twin %v: program = %q", hadTwin, got)
		}
		if got, _ := os.ReadFile(twin); string(got) != "the new chat client" {
			t.Errorf("had a twin %v: twin = %q, want the new chat client", hadTwin, got)
		}
		_, err := os.Stat(twin + ".old")
		if hadTwin != (err == nil) {
			t.Errorf("had a twin %v: the replaced twin moved aside = %v", hadTwin, err == nil)
		}
		Sweep(exe)
		if _, err := os.Stat(twin + ".old"); !os.IsNotExist(err) {
			t.Errorf("had a twin %v: Sweep left the replaced twin behind", hadTwin)
		}
	}
}

// The first update to a release with the twin is put in place by the release
// before, whose updater knows only the program, and a program replaced by
// hand leaves an old twin behind. At start the program makes the twin that
// belongs beside it from itself — the same bytes with the PE Subsystem field
// set to console — when it is missing or stale, and leaves one that is right,
// or that a pane has open, alone.
func TestEnsureChatTwinMakesTheTwinFromTheProgram(t *testing.T) {
	if chatName == "" {
		t.Skip("only the Windows release has a console twin")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	console, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	off, ok := subsystemOffset(console)
	if !ok || binary.LittleEndian.Uint16(console[off:]) != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		t.Fatal("the test binary is not a console PE file to start from")
	}
	gui := bytes.Clone(console)
	binary.LittleEndian.PutUint16(gui[off:], pe.IMAGE_SUBSYSTEM_WINDOWS_GUI)

	install := t.TempDir()
	exe, twin := filepath.Join(install, binaryName), filepath.Join(install, chatName)
	if err := os.WriteFile(exe, gui, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChatTwin(exe); err != nil {
		t.Fatalf("EnsureChatTwin: %v", err)
	}
	got, err := os.ReadFile(twin)
	if err != nil || !bytes.Equal(got, console) {
		t.Fatalf("the twin is not the program with only its subsystem set to console (%v)", err)
	}
	if out, err := exec.Command(twin, "-test.run=^$").CombinedOutput(); err != nil {
		t.Errorf("the twin made does not run: %v\n%s", err, out)
	}

	// One that is right is not written again.
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(twin, past, past); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChatTwin(exe); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(twin); err != nil || !fi.ModTime().Equal(past) {
		t.Errorf("a twin that was right was written again (modified %v)", fi.ModTime())
	}

	// A stale one, of another build, is replaced, but not while a pane has it
	// open: that is left without an error for the next start.
	if err := os.WriteFile(twin, []byte("an old chat client"), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(twin)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureChatTwin(exe); err != nil {
		t.Errorf("with the twin held: %v", err)
	}
	if got, _ := os.ReadFile(twin); string(got) != "an old chat client" {
		t.Errorf("a twin held open was written over")
	}
	held.Close()
	if err := EnsureChatTwin(exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(twin); !bytes.Equal(got, console) {
		t.Error("a stale twin was not replaced")
	}
	if left, _ := filepath.Glob(filepath.Join(install, chatName+".*.tmp")); len(left) > 0 {
		t.Errorf("left beside the twin: %v", left)
	}

	// The twin makes none from itself.
	if err := os.WriteFile(twin, []byte("the released twin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChatTwin(twin); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(twin); string(got) != "the released twin" {
		t.Error("the twin rewrote itself")
	}

	// Nothing but a regular file is written: not through a link, and not over
	// a directory of that name.
	odd := t.TempDir()
	if err := os.WriteFile(filepath.Join(odd, binaryName), gui, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(odd, chatName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChatTwin(filepath.Join(odd, binaryName)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(odd, chatName)); err != nil || !fi.IsDir() {
		t.Error("a directory where the twin goes was written over")
	}

	// A console build runs in a pane as it is.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, binaryName), console, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChatTwin(filepath.Join(other, binaryName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, chatName)); !os.IsNotExist(err) {
		t.Error("a console build was given a twin")
	}
}

// `flockdeck-chat update`, run from the twin, has to put each file in its own
// place: the program's over the program, the twin's over the twin. Taking the
// twin for the program left a GUI program where the panes need the console
// one, and the program as it was.
func TestApplyFromTheTwinPutsEachInItsPlace(t *testing.T) {
	if chatName == "" {
		t.Skip("only the Windows release has a console twin")
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir, install := t.TempDir(), t.TempDir()
	exe, twin := filepath.Join(install, binaryName), filepath.Join(install, chatName)
	write(exe, "the old program")
	write(twin, "the old chat client")
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(staging, binaryName), "the new program")
	write(filepath.Join(staging, chatName), "the new chat client")
	if err := save(dir, &Pending{Version: "v9.9.9", Binary: filepath.Join(staging, binaryName), Chat: filepath.Join(staging, chatName)}); err != nil {
		t.Fatal(err)
	}

	if err := Apply(dir, twin); err != nil {
		t.Fatalf("Apply from the twin: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "the new program" {
		t.Errorf("program = %q, want the new program", got)
	}
	if got, _ := os.ReadFile(twin); string(got) != "the new chat client" {
		t.Errorf("twin = %q, want the new chat client", got)
	}
}

// The program and its twin go in together or not at all: a program that
// cannot be put in place takes the twin that went in before it back out, and
// the update stays staged for the next attempt.
func TestApplyNeverLeavesAHalfUpdatedPair(t *testing.T) {
	if chatName == "" {
		t.Skip("only the Windows release has a console twin")
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, hadTwin := range []bool{true, false} {
		dir, install := t.TempDir(), t.TempDir()
		exe, twin := filepath.Join(install, binaryName), filepath.Join(install, chatName)
		write(exe, "the old program")
		if hadTwin {
			write(twin, "the old chat client")
		}
		staging := filepath.Join(dir, "staging")
		if err := os.MkdirAll(staging, 0o755); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(staging, binaryName), "the new program")
		write(filepath.Join(staging, chatName), "the new chat client")
		if err := save(dir, &Pending{Version: "v9.9.9", Binary: filepath.Join(staging, binaryName), Chat: filepath.Join(staging, chatName)}); err != nil {
			t.Fatal(err)
		}
		// Where the program's copy would land is taken, so only the program
		// fails to go in.
		if err := os.Mkdir(exe+".new", 0o755); err != nil {
			t.Fatal(err)
		}

		if err := Apply(dir, exe); err == nil {
			t.Fatalf("had a twin %v: Apply succeeded with the program unable to go in", hadTwin)
		}
		if got, _ := os.ReadFile(exe); string(got) != "the old program" {
			t.Errorf("had a twin %v: program = %q, want the old one", hadTwin, got)
		}
		got, err := os.ReadFile(twin)
		switch {
		case hadTwin && string(got) != "the old chat client":
			t.Errorf("twin = %q, %v; want the old one put back", got, err)
		case !hadTwin && err == nil:
			t.Errorf("a twin the installation never had was left in place: %q", got)
		}
		if _, ok := Load(dir); !ok {
			t.Errorf("had a twin %v: the update is no longer staged for the next attempt", hadTwin)
		}
	}
}

// holdWhileRenamed has the first file match picks out, among those renamed,
// held open for a tenth of a second from just before its rename, as a virus
// scanner holds a program it has just been shown. It reports whether that
// hold was taken, so a rename that went around it cannot pass unnoticed.
func holdWhileRenamed(t *testing.T, match func(src string) bool) *atomic.Bool {
	t.Helper()
	var took atomic.Bool
	old := rename
	rename = func(src, dst string) error {
		if match(src) && took.CompareAndSwap(false, true) {
			f, err := os.Open(src)
			if err != nil {
				return err
			}
			time.AfterFunc(100*time.Millisecond, func() { f.Close() })
		}
		return old(src, dst)
	}
	t.Cleanup(func() { rename = old })
	return &took
}

// The new program is landed beside the old one and then renamed into place,
// and a scanner holding it at that moment made the rename fail: the update
// was rolled back, with advice to try from an administrator shell that would
// have changed nothing. The rename waits the hold out.
func TestApplyWaitsOutAMomentsHoldOnTheNewProgram(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to rename a file that is open")
	}
	// Apply resolves the program's path before landing the new one beside it,
	// and on Windows that spells out a temporary directory given by its short
	// name, as CI's runners give theirs. The hold is looked for under the name
	// Apply will use, or it is never taken there.
	install, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(install, binaryName)
	if err := os.WriteFile(exe, []byte("the old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staging", binaryName)
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("the new program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := save(dir, &Pending{Version: "v9.9.9", Binary: staged}); err != nil {
		t.Fatal(err)
	}

	// The hold is taken in the hook itself, before the rename it wraps runs,
	// so it is always in place when the rename comes.
	took := holdWhileRenamed(t, func(src string) bool { return src == exe+".new" })
	if err := Apply(dir, exe); err != nil {
		t.Fatalf("Apply with the new program held open for a moment: %v", err)
	}
	if !took.Load() {
		t.Fatal("the new program was never renamed into place")
	}
	if got, _ := os.ReadFile(exe); string(got) != "the new program" {
		t.Errorf("installed binary = %q, want the new program", got)
	}
}

// The twin EnsureChatTwin writes is renamed into place just as the program
// is, and a scanner holding it at that moment left the installation without a
// twin until the next start: an API agent's pane blank meanwhile.
func TestEnsureChatTwinWaitsOutAMomentsHoldOnTheTwinItWrote(t *testing.T) {
	if chatName == "" {
		t.Skip("only the Windows release has a console twin")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	console, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	off, ok := subsystemOffset(console)
	if !ok || binary.LittleEndian.Uint16(console[off:]) != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		t.Fatal("the test binary is not a console PE file to start from")
	}
	gui := bytes.Clone(console)
	binary.LittleEndian.PutUint16(gui[off:], pe.IMAGE_SUBSYSTEM_WINDOWS_GUI)
	install := t.TempDir()
	exe, twin := filepath.Join(install, binaryName), filepath.Join(install, chatName)
	if err := os.WriteFile(exe, gui, 0o755); err != nil {
		t.Fatal(err)
	}

	took := holdWhileRenamed(t, func(src string) bool { return strings.HasSuffix(src, ".tmp") })
	if err := EnsureChatTwin(exe); err != nil {
		t.Fatalf("EnsureChatTwin: %v", err)
	}
	if !took.Load() {
		t.Fatal("the twin written was never renamed into place")
	}
	if got, err := os.ReadFile(twin); err != nil || !bytes.Equal(got, console) {
		t.Errorf("a twin held for a moment as it was written was not put in place (%v)", err)
	}
}

func TestApplyLeavesTheProgramInPlaceWhenNothingIsStaged(t *testing.T) {
	install := t.TempDir()
	exe := filepath.Join(install, binaryName)
	if err := os.WriteFile(exe, []byte("the old program"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Apply(t.TempDir(), exe); err == nil {
		t.Fatal("Apply reported success with nothing staged")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "the old program" {
		t.Errorf("installed binary = %q; a failed Apply must not disturb it", got)
	}
}

func TestLoadIgnoresARecordWhoseDownloadHasGone(t *testing.T) {
	dir := t.TempDir()
	if err := save(dir, &Pending{Version: "v9.9.9", Binary: filepath.Join(dir, "staging", binaryName)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load(dir); ok {
		t.Error("an update whose binary is missing was reported as ready to apply")
	}
}

// A rate limit is GitHub saying "not now", and the message has to say that
// rather than a bare 403 that reads as a broken release.
func TestGetExplainsARateLimit(t *testing.T) {
	reset := time.Now().Add(20 * time.Minute)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("get succeeded against a rate limit")
	}
	if msg := err.Error(); !strings.Contains(msg, "try again after "+reset.Format("15:04")) || strings.Contains(msg, "403") {
		t.Errorf("err = %q, want it to say when to try again rather than quote the status", msg)
	}
}

// What `flockdeck update` says it is about to download is this platform's
// archive, not whichever asset came first.
func TestDownloadSizeIsThisPlatformsArchive(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: "flockdeck_v9.9.9_plan9_mips.tar.gz", Size: 1},
		{Name: "flockdeck_v9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip", Size: 7 << 20},
		{Name: "checksums.txt", Size: 2},
	}}
	if got := rel.DownloadSize(); got != 7<<20 {
		t.Errorf("DownloadSize = %d, want this platform's archive", got)
	}
	if got := (&Release{}).DownloadSize(); got != 0 {
		t.Errorf("DownloadSize of a release with nothing for this platform = %d, want 0", got)
	}
}

func TestNewerIgnoresAnUntaggedLocalBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Version: "v9.9.9"})
	}))
	defer srv.Close()

	rel := fetchRelease(t, srv.URL)
	if Newer(rel.Version, "dev") {
		t.Error("a published release was treated as newer than an untagged local build")
	}
}
