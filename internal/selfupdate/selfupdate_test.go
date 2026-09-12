package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	if runtime.GOOS == "windows" {
		bw := &byteWriter{}
		zw := zip.NewWriter(bw)
		h := &zip.FileHeader{Name: binaryName, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return "flockdeck_v9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip", bw.b
	}

	bw := &byteWriter{}
	gz := gzip.NewWriter(bw)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: binaryName, Mode: 0o755, Size: int64(len(body)), Format: tar.FormatPAX,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
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

// releaseServer stands in for GitHub, serving one release whose checksums file
// can be made to disagree with the archive so the checking path can be tested.
func releaseServer(t *testing.T, archiveName string, archive []byte, sum string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sum, archiveName)
	})
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{
			Version: "v9.9.9",
			Notes:   "notes",
			URL:     "https://example.invalid/rel",
			Assets: []Asset{
				{Name: archiveName, URL: srv.URL + "/archive"},
				{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"},
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

func TestCheckIgnoresAnUntaggedLocalBuild(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Version: "v9.9.9"})
	}))
	defer srv.Close()

	rel := fetchRelease(t, srv.URL)
	if Newer(rel.Version, "dev") {
		t.Error("a published release was treated as newer than an untagged local build")
	}
}
