package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// releases stands in for everywhere the install scripts download from, under
// one server: the site at /dl, GitHub's downloads at /gh and a mirror of
// somebody's own at /mirror. Every place serves the same bytes for an
// archive, as the real ones do, and the server records which place served
// each, so a test can tell where an installation was fetched.
type releases struct {
	srv *httptest.Server

	mu       sync.Mutex
	onSite   map[string]bool // versions the site has; GitHub has every one
	down     bool            // the site drops every connection
	stalled  bool            // the site takes every connection and never answers
	tampered bool            // the site serves an archive its checksums do not describe
	forged   string          // a place that serves a tampered archive, and checksums to match it
	latest   string          // what dl.flockdeck.ai's latest.json names; "" answers with nothing
	ghLatest string          // what GitHub's "latest release" API names; "" answers with nothing
	program  []byte          // the program the archives carry; installArchive's plain text if nil
	asked    []string
	served   []string // the places an archive was served from
}

var archiveName = regexp.MustCompile(`^flockdeck_(v[^_]+)_([a-z]+)_([a-z0-9]+)\.(tar\.gz|zip)$`)

// darwinBundleExe is where install.sh puts the program inside Flockdeck.app,
// relative to the directory the bundle itself is installed into.
const darwinBundleExe = "Flockdeck.app/Contents/MacOS/flockdeck"

// installedExe is where install.sh puts the program under bin, the
// directory it was told (or defaulted) to install into: install.sh's own
// detect_os decides this at run time from the real machine's uname, so the
// tests that run it, all on runtime.GOOS since installShRunner skips
// Windows, need the same split to know where to look afterward.
func installedExe(bin string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(bin, filepath.FromSlash(darwinBundleExe))
	}
	return filepath.Join(bin, "flockdeck")
}

// defaultInstallDir is where install.sh installs without FLOCKDECK_INSTALL_DIR
// set, given HOME.
func defaultInstallDir(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Applications")
	}
	return filepath.Join(home, ".local", "bin")
}

// testRelease is the release the tests generate the site for: v9.9.9, which
// the site and GitHub both have, with the checksums of its archives as they
// serve them.
func testRelease() release {
	return testReleaseWithProgram(nil)
}

// testReleaseWithProgram is testRelease for a release whose archives carry
// program instead of installArchive's plain text -- what the tests that run
// the installed binary's own `update` give it, since those have to be able to
// execute what the script installs, not merely read its bytes.
func testReleaseWithProgram(program []byte) release {
	rel := release{Version: "v9.9.9", Sums: map[string]string{}}
	for _, name := range archives(rel.Version) {
		sum := sha256.Sum256(installArchiveWithProgram(name, program))
		rel.Sums[name] = hex.EncodeToString(sum[:])
	}
	return rel
}

func newReleases(t *testing.T) *releases {
	t.Helper()
	r := &releases{onSite: map[string]bool{"v9.9.9": true, "v9.9.8": true}}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *releases) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.asked = append(r.asked, req.URL.Path)
	onSite, down, stalled, tampered, forged, latest, ghLatest, program :=
		r.onSite, r.down, r.stalled, r.tampered, r.forged, r.latest, r.ghLatest, r.program
	r.mu.Unlock()
	archiveOf := func(name string) []byte { return installArchiveWithProgram(name, program) }

	place, rest, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/")
	if place == "gh" {
		rest = strings.TrimPrefix(rest, defaultRepo[len("https://github.com/"):]+"/releases/download/")
	}
	switch {
	case place == "dl" && down:
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			conn.Close()
		}
		return
	case place == "dl" && stalled:
		// Held open, with nothing sent, until the client hangs up. A
		// hijacked connection is the server's no longer, so closing the
		// server does not wait on one a client never gives up.
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			go func() {
				io.Copy(io.Discard, conn)
				conn.Close()
			}()
		}
		return
	// latest.json, the one file dl.flockdeck.ai carries outside a version's
	// own directory, and its counterpart on GitHub's API: what the scripts
	// read to decide whether to move the pinned release they just installed
	// on to something newer. Neither is signed, matching the real ones
	// (internal/selfupdate/site.go), since nothing here is trusted beyond
	// deciding whether to ask -- the installed binary's own `update` checks
	// the release signature. Answering with nothing, the default, is what
	// every test that does not care about this gets.
	case place == "dl" && rest == "latest.json":
		if latest == "" {
			http.NotFound(w, req)
			return
		}
		fmt.Fprintf(w, `{"version":%q}`, latest)
		return
	case place == "ghapi":
		if rest != "repos/"+defaultRepo[len("https://github.com/"):]+"/releases/latest" || ghLatest == "" {
			http.NotFound(w, req)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q}`, ghLatest)
		return
	case place != "dl" && place != "gh" && place != "mirror":
		http.NotFound(w, req)
		return
	}
	version, file, ok := strings.Cut(rest, "/")
	if !ok || (place == "dl" && !onSite[version]) {
		http.NotFound(w, req)
		return
	}
	// A tampered archive is another release's, served under this one's name.
	evil := func(name string) string { return strings.Replace(name, version, version+"-evil", 1) }
	if file == "checksums.txt" {
		// Every platform, so that whatever machine runs the test finds its own.
		for _, p := range []string{"linux_amd64.tar.gz", "linux_arm64.tar.gz", "darwin_amd64.tar.gz", "darwin_arm64.tar.gz", "windows_amd64.zip", "windows_arm64.zip"} {
			name := "flockdeck_" + version + "_" + p
			described := name
			if place == forged {
				described = evil(name)
			}
			sum := sha256.Sum256(archiveOf(described))
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		}
		return
	}
	if !archiveName.MatchString(file) {
		http.NotFound(w, req)
		return
	}
	if (place == "dl" && tampered) || place == forged {
		file = evil(file)
	}
	r.mu.Lock()
	r.served = append(r.served, place)
	r.mu.Unlock()
	w.Write(archiveOf(file))
}

// askedOf is every path requested under one place.
func (r *releases) askedOf(place string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var got []string
	for _, p := range r.asked {
		if strings.HasPrefix(p, "/"+place+"/") {
			got = append(got, p)
		}
	}
	return got
}

// servedFrom is every place that served an archive, in order.
func (r *releases) servedFrom() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.served)
}

// installArchive is the archive of that name: the same bytes every time,
// holding a program that names its version.
func installArchive(name string) []byte {
	return installArchiveWithProgram(name, nil)
}

// installArchiveWithProgram is installArchive, except the archive holds
// program in place of the usual plain text, when program is not nil. The
// tests that ask the script to run what it just installed need a real
// executable there; every other test only ever reads the file back.
func installArchiveWithProgram(name string, program []byte) []byte {
	var buf bytes.Buffer
	m := archiveName.FindStringSubmatch(name)
	body := program
	if body == nil {
		body = []byte("flockdeck " + m[1])
	}
	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("flockdeck.exe")
		w.Write(body)
		if program != nil {
			// Real releases carry the console twin beside the program
			// (selfupdate.chatName); the tests that run what the script
			// installs need it there too, to exercise install.ps1 running
			// `update` through it.
			w, _ = zw.Create("flockdeck-chat.exe")
			w.Write(body)
		}
		zw.Close()
		return buf.Bytes()
	}

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if m[2] == "darwin" {
		// A real macOS archive carries a whole Flockdeck.app rather than a
		// bare binary (cmd/release's own buildDarwinBundle); install.sh
		// unpacks it the same way, naming the bundle directory instead of a
		// single file.
		for _, dir := range []string{"Flockdeck.app/", "Flockdeck.app/Contents/", "Flockdeck.app/Contents/MacOS/", "Flockdeck.app/Contents/Resources/"} {
			tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755, Format: tar.FormatPAX})
		}
		tw.WriteHeader(&tar.Header{Name: darwinBundleExe, Mode: 0o755, Size: int64(len(body)), Format: tar.FormatPAX})
		tw.Write(body)
		icon := []byte("not really an icns, just something for install.sh to find")
		tw.WriteHeader(&tar.Header{Name: "Flockdeck.app/Contents/Resources/AppIcon.icns", Mode: 0o644, Size: int64(len(icon)), Format: tar.FormatPAX})
		tw.Write(icon)
		plist := []byte("not really a plist, just something for install.sh to find")
		tw.WriteHeader(&tar.Header{Name: "Flockdeck.app/Contents/Info.plist", Mode: 0o644, Size: int64(len(plist)), Format: tar.FormatPAX})
		tw.Write(plist)
	} else {
		tw.WriteHeader(&tar.Header{Name: "flockdeck", Mode: 0o755, Size: int64(len(body)), Format: tar.FormatPAX})
		tw.Write(body)
		// install.sh unpacks this alongside the binary on Linux, for the
		// desktop entry it writes; a real archive's is cmd/release's copy of
		// build/appicon.png, but nothing here reads these bytes as an image.
		icon := []byte("not really a png, just something for install.sh to find")
		tw.WriteHeader(&tar.Header{Name: "flockdeck.png", Mode: 0o644, Size: int64(len(icon)), Format: tar.FormatPAX})
		tw.Write(icon)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// pointAt is an install script as the site ships it, with where it
// downloads from pointed at r. Each address must be found exactly once, so a
// script that stops naming them as it did fails here rather than reaching the
// real ones.
func pointAt(t *testing.T, script string, r *releases, lines map[string]string) string {
	t.Helper()
	for from, to := range lines {
		if strings.Count(script, from) != 1 {
			t.Fatalf("the script does not say %s exactly once", from)
		}
		script = strings.Replace(script, from, fmt.Sprintf(to, r.srv.URL), 1)
	}
	return script
}

// shAddresses and ps1Addresses are where each script downloads from, and what
// pointAt makes of each.
var (
	shAddresses = map[string]string{
		`DL="https://dl.flockdeck.ai"`:        `DL="%s/dl"`,
		`GITHUB="https://github.com"`:         `GITHUB="%s/gh"`,
		`GITHUB_API="https://api.github.com"`: `GITHUB_API="%s/ghapi"`,
	}
	ps1Addresses = map[string]string{
		`$dl = 'https://dl.flockdeck.ai'`:       `$dl = '%s/dl'`,
		`$github = 'https://github.com'`:        `$github = '%s/gh'`,
		`$githubApi = 'https://api.github.com'`: `$githubApi = '%s/ghapi'`,
	}
)

// installCase is one run of an install script: what it is given, where the
// program should come from, and what it should say.
type installCase struct {
	name    string
	setup   func(r *releases)
	env     map[string]string
	from    string   // the place the program was fetched from; "" for a failure
	version string   // the version installed
	says    string   // part of what the script prints
	never   []string // places that must not have been asked anything
}

func installCases() []installCase {
	return []installCase{
		{name: "from the site", from: "dl", version: "v9.9.9", never: []string{"gh"}},
		{name: "the site out of reach", setup: func(r *releases) { r.down = true },
			from: "gh", version: "v9.9.9", says: "downloading it from GitHub instead"},
		{name: "a release older than the site", env: map[string]string{"FLOCKDECK_VERSION": "0.1.0"},
			from: "gh", version: "v0.1.0", says: "downloading it from GitHub instead"},
		{name: "another release chosen by hand", env: map[string]string{"FLOCKDECK_VERSION": "v9.9.8"},
			from: "dl", version: "v9.9.8", says: "checked only against the checksums.txt downloaded beside it", never: []string{"gh"}},
		{name: "a mirror", env: map[string]string{"FLOCKDECK_DOWNLOAD": "%s/mirror/"},
			from: "mirror", version: "v9.9.9", never: []string{"dl", "gh"}},
		{name: "a tampered archive on the site", setup: func(r *releases) { r.tampered = true },
			says: "does not match its published checksum", never: []string{"gh"}},
		// The site's checksums.txt is replaced along with the archive, as it
		// would be by whoever could write the one: the archive is still
		// checked against the checksum the script itself carries.
		{name: "a tampered archive on the site with checksums to match", setup: func(r *releases) { r.forged = "dl" },
			says: "does not match its published checksum", never: []string{"gh"}},
		{name: "a tampered archive of the script's own release chosen by hand", setup: func(r *releases) { r.forged = "dl" },
			env:  map[string]string{"FLOCKDECK_VERSION": "9.9.9"},
			says: "does not match its published checksum", never: []string{"gh"}},
		{name: "a tampered archive on GitHub with checksums to match", setup: func(r *releases) { r.down, r.forged = true, "gh" },
			says: "does not match its published checksum"},
		{name: "a tampered archive of another release chosen by hand", setup: func(r *releases) { r.tampered = true },
			env:  map[string]string{"FLOCKDECK_VERSION": "v9.9.8"},
			says: "does not match its published checksum", never: []string{"gh"}},
	}
}

// check runs the expectations of one case against what a run did.
func (c installCase) check(t *testing.T, r *releases, installed string, out []byte, err error) {
	t.Helper()
	got, readErr := os.ReadFile(installed)
	if c.from == "" {
		if err == nil || readErr == nil {
			t.Errorf("the script installed %q and exited %v; want it refused\n%s", got, err, out)
		}
	} else {
		if err != nil || string(got) != "flockdeck "+c.version {
			t.Errorf("installed %q (%v); want %s\n%s", got, err, c.version, out)
		}
		if served := r.servedFrom(); !slices.Equal(served, []string{c.from}) {
			t.Errorf("the archive was served from %v; want %s alone\n%s", served, c.from, out)
		}
	}
	if c.says != "" && !strings.Contains(string(out), c.says) {
		t.Errorf("the script said %q; want %q in it", out, c.says)
	}
	for _, p := range c.never {
		if asked := r.askedOf(p); len(asked) > 0 {
			t.Errorf("asked %v of %s", asked, p)
		}
	}
}

// installEnv is the environment a script runs in: this one, less any
// FLOCKDECK_ setting of the machine's own, plus the case's.
func installEnv(r *releases, extra map[string]string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "FLOCKDECK_") {
			env = append(env, kv)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+strings.ReplaceAll(v, "%s", r.srv.URL))
	}
	return env
}

// install.sh downloads the release it was published with from the site, and
// goes to GitHub for it when the site cannot give it; a mirror given by hand is
// the only place asked. An archive that does not match the checksum the script
// carries is never installed, whatever the checksums.txt beside it says, and
// one of another release chosen by hand is checked against that release's
// checksums.txt.
func TestInstallShDownloadsFromTheSite(t *testing.T) {
	sh := installShRunner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range installCases() {
		t.Run(c.name, func(t *testing.T) {
			r := newReleases(t)
			if c.setup != nil {
				c.setup(r)
			}
			script := pointAt(t, string(shipped), r, shAddresses)
			home := t.TempDir()
			path := filepath.Join(home, "install.sh")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(home, "bin")
			cmd := exec.Command(sh, path)
			cmd.Env = append(installEnv(r, c.env), "HOME="+home, "FLOCKDECK_INSTALL_DIR="+bin)
			out, err := cmd.CombinedOutput()
			c.check(t, r, installedExe(bin), out, err)
		})
	}
}

// installShRunner is the sh to run install.sh with, and skips a test on a
// machine without it or the tools the script needs.
func installShRunner(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh sends Windows to install.ps1")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to run it with")
	}
	for _, tools := range [][]string{{"curl", "wget"}, {"sha256sum", "shasum"}, {"tar"}} {
		found := false
		for _, tool := range tools {
			if _, err := exec.LookPath(tool); err == nil {
				found = true
			}
		}
		if !found {
			t.Skipf("no %s to run install.sh with", strings.Join(tools, " or "))
		}
	}
	return sh
}

// A directory given with a slash on the end is the same directory, and so is
// one on PATH written with one. Compared as typed, one on PATH was "not on
// your PATH", and the flockdeck just put there was "not this one". A HOME
// ending in a slash made the default ~//.local/bin, which did the same.
func TestInstallShTakesADirectoryWithASlashOnTheEnd(t *testing.T) {
	sh := installShRunner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := newReleases(t)
	script := pointAt(t, string(shipped), r, shAddresses)
	for _, c := range []struct {
		name string
		// Each is given the test's home directory and says what to set:
		// HOME, the install directory ("" for the default), and the
		// directory on PATH.
		env func(home string) (homeVar, installDir, onPath string)
	}{
		{"given with a slash", func(home string) (string, string, string) {
			return home, home + "/bin/", home + "/bin"
		}},
		{"given and on PATH with a slash", func(home string) (string, string, string) {
			return home, home + "/bin/", home + "/bin/"
		}},
		{"on PATH with a slash", func(home string) (string, string, string) {
			return home, home + "/bin", home + "/bin//"
		}},
		{"the default under a HOME with a slash", func(home string) (string, string, string) {
			return home + "/", "", home + "/.local/bin"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "install.sh")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			homeVar, installDir, onPath := c.env(home)
			cmd := exec.Command(sh, path)
			cmd.Env = append(installEnv(r, nil), "HOME="+homeVar,
				"PATH="+onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
			bin := defaultInstallDir(home)
			if installDir != "" {
				cmd.Env = append(cmd.Env, "FLOCKDECK_INSTALL_DIR="+installDir)
				bin = filepath.Join(home, "bin")
			}
			want := installedExe(bin)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if _, err := os.Stat(want); err != nil {
				t.Errorf("nothing installed at %s: %v\n%s", want, err, out)
			}
			// macOS never prints a PATH-based "start it with": a
			// double-clickable Flockdeck.app is opened from Launchpad,
			// Spotlight or Finder instead.
			wantSaid := "start it with: flockdeck\n"
			if runtime.GOOS == "darwin" {
				wantSaid = "open it from Launchpad, Spotlight or Finder"
			}
			if !strings.Contains(string(out), wantSaid) ||
				strings.Contains(string(out), "not on your PATH") || strings.Contains(string(out), "not this one") {
				t.Errorf("HOME=%s, FLOCKDECK_INSTALL_DIR=%s, and %s on PATH said:\n%s", homeVar, installDir, onPath, out)
			}
		})
	}
}

// With neither curl nor wget, the first fetch's complaint was thrown away with
// the rest of what an unreachable site says: the script said the site could
// not be reached and that it was going to GitHub instead, before saying what
// was missing.
func TestInstallShSaysWhenItHasNothingToDownloadWith(t *testing.T) {
	sh := installShRunner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := newReleases(t)
	script := pointAt(t, string(shipped), r, shAddresses)
	home := t.TempDir()
	path := filepath.Join(home, "install.sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := toolsDir(t, home)
	cmd := exec.Command(sh, path)
	cmd.Env = append(installEnv(r, nil), "HOME="+home, "FLOCKDECK_INSTALL_DIR="+filepath.Join(home, "bin"), "PATH="+tools)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the script succeeded with nothing to download with:\n%s", out)
	}
	if !strings.Contains(string(out), "needs curl or wget") || strings.Contains(string(out), "could not reach") {
		t.Errorf("with neither curl nor wget the script said:\n%s", out)
	}
	for _, p := range []string{"dl", "gh"} {
		if asked := r.askedOf(p); len(asked) > 0 {
			t.Errorf("asked %v of %s", asked, p)
		}
	}
}

// toolsDir is a directory, under home, of links to every program install.sh
// uses but the two that download, and to those named in extra: a PATH of it
// alone gives the script the downloaders a test chooses and no others.
func toolsDir(t *testing.T, home string, extra ...string) string {
	t.Helper()
	tools := filepath.Join(home, "tools")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"uname", "sysctl", "mktemp", "rm", "sed", "head", "cut", "awk",
		"tar", "gzip", "sha256sum", "shasum", "mkdir", "cp", "chmod", "mv"}, extra...) {
		if p, err := exec.LookPath(name); err == nil {
			if err := os.Symlink(p, filepath.Join(tools, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return tools
}

// A site that is down, or that takes the connection and then sends nothing,
// is given up on in seconds, and GitHub asked instead. GNU wget retries 20
// times by default, waiting longer after each, so a site that was down took
// over two minutes to give up on; curl's --connect-timeout covers only the
// connection, so a site that stalled after it held the script for good.
func TestInstallShGivesUpOnTheSiteInSeconds(t *testing.T) {
	sh := installShRunner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, tool string
		setup      func(r *releases)
	}{
		{"wget with the site down", "wget", func(r *releases) { r.down = true }},
		{"curl with the site stalled", "curl", func(r *releases) { r.stalled = true }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := exec.LookPath(c.tool); err != nil {
				t.Skipf("no %s", c.tool)
			}
			r := newReleases(t)
			c.setup(r)
			script := pointAt(t, string(shipped), r, shAddresses)
			// A download is allowed five minutes, which is how long a stall
			// takes to end. Here it is allowed three seconds, so that the
			// test sees it end without waiting that long.
			script = strings.Replace(script, "--max-time 300", "--max-time 3", 1)
			home := t.TempDir()
			path := filepath.Join(home, "install.sh")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(home, "bin")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, sh, path)
			// A downloader left running by a killed script would hold the
			// output open, and the wait with it.
			cmd.WaitDelay = 5 * time.Second
			cmd.Env = append(installEnv(r, nil), "HOME="+home, "FLOCKDECK_INSTALL_DIR="+bin, "PATH="+toolsDir(t, home, c.tool))
			start := time.Now()
			out, err := cmd.CombinedOutput()
			took := time.Since(start)
			got, _ := os.ReadFile(installedExe(bin))
			if err != nil || string(got) != "flockdeck v9.9.9" {
				t.Fatalf("after %v the script exited %v and installed %q\n%s", took.Round(time.Second), err, got, out)
			}
			if took > 30*time.Second {
				t.Errorf("the script took %v to give up on the site", took.Round(time.Second))
			}
			if served := r.servedFrom(); !slices.Equal(served, []string{"gh"}) {
				t.Errorf("the archive was served from %v; want gh alone\n%s", served, out)
			}
		})
	}
}

// With another flockdeck first on PATH, the script says to start the one it
// installed by its path, and a directory with a space in it split that path
// into two words when pasted.
func TestInstallShQuotesThePathItSaysToStart(t *testing.T) {
	sh := installShRunner(t)
	if runtime.GOOS == "darwin" {
		t.Skip("macOS opens Flockdeck.app from Launchpad, Spotlight or Finder; it never prints a PATH-shadowed start command")
	}
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := newReleases(t)
	script := pointAt(t, string(shipped), r, shAddresses)
	home := t.TempDir()
	path := filepath.Join(home, "install.sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	// Another flockdeck, found first: a go install, say.
	other := filepath.Join(home, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "flockdeck"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "my bin")
	cmd := exec.Command(sh, path)
	sep := string(os.PathListSeparator)
	cmd.Env = append(installEnv(r, nil), "HOME="+home, "FLOCKDECK_INSTALL_DIR="+bin,
		"PATH="+other+sep+bin+sep+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if want := `start it with: "` + filepath.Join(bin, "flockdeck") + `"`; !strings.Contains(string(out), want) {
		t.Errorf("want %q in what the script said:\n%s", want, out)
	}
}

// On Linux, install.sh unpacks the icon its archive carries alongside the
// binary and gives it, and a .desktop file, to $HOME/.local/share (or
// XDG_DATA_HOME), so Flockdeck shows up in an app menu without anyone
// needing to know it can also be started from a shell. macOS gets neither of
// those from here: Flockdeck.app already gets the same Dock/Spotlight/
// Launchpad visibility on its own, with no XDG-style entry to write -- the
// same run of this script on a macOS test runner has to confirm exactly
// that nothing was written here, not just skip checking it.
func TestInstallShAddsALinuxDesktopEntry(t *testing.T) {
	sh := installShRunner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := newReleases(t)
	script := pointAt(t, string(shipped), r, shAddresses)
	home := t.TempDir()
	path := filepath.Join(home, "install.sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	cmd := exec.Command(sh, path)
	cmd.Env = append(installEnv(r, nil), "HOME="+home, "FLOCKDECK_INSTALL_DIR="+bin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	entry := filepath.Join(home, ".local", "share", "applications", "flockdeck.desktop")
	icon := filepath.Join(home, ".local", "share", "icons", "flockdeck.png")
	if runtime.GOOS != "linux" {
		for _, p := range []string{entry, icon} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s exists on %s, which install.sh never writes an XDG-style desktop entry or icon for", p, runtime.GOOS)
			}
		}
		return
	}

	if !strings.Contains(string(out), "added an app-menu entry") {
		t.Errorf("the script did not say it added an app-menu entry:\n%s", out)
	}
	body, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("no desktop entry at %s: %v\n%s", entry, err, out)
	}
	want := "Exec=" + filepath.Join(bin, "flockdeck")
	if !strings.Contains(string(body), "Name=Flockdeck") || !strings.Contains(string(body), want) {
		t.Errorf("%s = %q, want it to name Flockdeck and %q", entry, body, want)
	}
	if _, err := os.Stat(icon); err != nil {
		t.Errorf("no icon at %s: %v", icon, err)
	}
}

// install.ps1 does as install.sh does. It runs where Windows PowerShell does,
// installing into a temporary directory and leaving PATH and the Start menu
// alone.
func TestInstallPs1DownloadsFromTheSite(t *testing.T) {
	ps := installPs1Runner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range installCases() {
		t.Run(c.name, func(t *testing.T) {
			r := newReleases(t)
			if c.setup != nil {
				c.setup(r)
			}
			script := pointAt(t, string(shipped), r, ps1Addresses)
			home := t.TempDir()
			path := filepath.Join(home, "install.ps1")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(home, "bin")
			cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			cmd.Env = append(installEnv(r, c.env), "FLOCKDECK_INSTALL_DIR="+bin, "FLOCKDECK_NO_MODIFY_PATH=1")
			out, err := cmd.CombinedOutput()
			c.check(t, r, filepath.Join(bin, "flockdeck.exe"), out, err)
		})
	}
}

// installPs1Runner is the Windows PowerShell to run install.ps1 with, and
// skips a test where there is none.
func installPs1Runner(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 reads the machine's architecture from the Windows registry")
	}
	ps, err := exec.LookPath("powershell")
	if err != nil {
		t.Skip("no Windows PowerShell to run it with")
	}
	return ps
}

// With FLOCKDECK_NO_MODIFY_PATH=1, install.ps1 leaves PATH alone, and it said
// nothing of how to start what it had installed, which `flockdeck` does not
// find in a directory PATH does not name. It says to start it by its path,
// quoted for PowerShell: inside single quotes an apostrophe is written as two,
// or the line it prints ends its string at the apostrophe and does not parse.
func TestInstallPs1SaysHowToStartItWithPathLeftAlone(t *testing.T) {
	ps := installPs1Runner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, dir string }{
		{"a plain directory", "bin"},
		{"a directory with an apostrophe", "o'brien's bin"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newReleases(t)
			script := pointAt(t, string(shipped), r, ps1Addresses)
			home := t.TempDir()
			path := filepath.Join(home, "install.ps1")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(home, c.dir)
			cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			// A PATH with no flockdeck on it, whatever this machine has.
			sys := os.Getenv("SystemRoot")
			cmd.Env = append(installEnv(r, nil), "FLOCKDECK_INSTALL_DIR="+bin, "FLOCKDECK_NO_MODIFY_PATH=1",
				"PATH="+filepath.Join(sys, "System32")+string(os.PathListSeparator)+sys)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			want := "flockdeck: start it with: & '" + strings.ReplaceAll(filepath.Join(bin, "flockdeck.exe"), "'", "''") + "'"
			if !strings.Contains(string(out), want) {
				t.Errorf("want %q in what the script said:\n%s", want, out)
			}
		})
	}
}

// A directory given with a slash on the end is the same directory, and so is
// one on PATH written with one. Compared as typed, C:\Tools\ was added to the
// user's PATH beside the C:\Tools already on it, and C:\Tools beside a
// C:\Tools\. The script's PATH, Start menu shortcut and change broadcast are
// pointed at a scratch registry key and folder here, so that the user's own
// are left alone.
func TestInstallPs1TakesADirectoryWithASlashOnTheEnd(t *testing.T) {
	ps := installPs1Runner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	powershell := func(script string) string {
		t.Helper()
		out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
		if err != nil {
			t.Fatalf("powershell: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, c := range []struct{ name, given, onPath string }{
		{"given with a slash", `bin\`, `bin`},
		{"on PATH with a slash", `bin`, `bin\`},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newReleases(t)
			home := t.TempDir()
			// Joined by hand: filepath.Join would take the slash off.
			given, onPath := home+`\`+c.given, home+`\`+c.onPath
			key := `HKCU:\Software\FlockdeckInstallTest-` + filepath.Base(filepath.Dir(home))
			programs := filepath.Join(home, "Programs")
			if err := os.Mkdir(programs, 0o755); err != nil {
				t.Fatal(err)
			}
			script := pointAt(t, string(shipped), r, ps1Addresses)
			for from, to := range map[string]string{
				`'HKCU:\Environment'`:                      "'" + key + "'",
				`[Environment]::GetFolderPath('Programs')`: "'" + programs + "'",
				`'FLOCKDECK_INSTALLING', '1', 'User'`:      `'FLOCKDECK_INSTALLING', '1', 'Process'`,
				`'FLOCKDECK_INSTALLING', $null, 'User'`:    `'FLOCKDECK_INSTALLING', $null, 'Process'`,
			} {
				if !strings.Contains(script, from) {
					t.Fatalf("the script no longer says %s, which this test points elsewhere", from)
				}
				script = strings.ReplaceAll(script, from, to)
			}
			path := filepath.Join(home, "install.ps1")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			const others = `C:\Windows`
			powershell(fmt.Sprintf(`New-Item -Path '%s' -Force | Out-Null; Set-ItemProperty -Path '%s' -Name Path -Value '%s' -Type String`,
				key, key, onPath+";"+others))
			t.Cleanup(func() {
				exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", "Remove-Item -Path '"+key+"' -Recurse -Force").Run()
			})

			cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			// A PATH with no flockdeck on it, whatever this machine has.
			sys := os.Getenv("SystemRoot")
			cmd.Env = append(installEnv(r, nil), "FLOCKDECK_INSTALL_DIR="+given,
				"PATH="+filepath.Join(sys, "System32")+string(os.PathListSeparator)+sys)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if _, err := os.Stat(filepath.Join(home, "bin", "flockdeck.exe")); err != nil {
				t.Errorf("nothing installed: %v\n%s", err, out)
			}
			got := powershell(fmt.Sprintf(`(Get-Item '%s').GetValue('Path', '', 'DoNotExpandEnvironmentNames')`, key))
			if want := onPath + ";" + others; got != want {
				t.Errorf("FLOCKDECK_INSTALL_DIR=%s with %s on PATH made PATH %q; want it left as %q\n%s", given, onPath, got, want, out)
			}
		})
	}
}

// install.ps1 calls nothing that PowerShell loads from a script module on
// first use. Windows PowerShell looks those up on PSModulePath, and one started
// from PowerShell 7 inherits a PSModulePath that finds PowerShell 7's copies
// first, which it cannot load: the command is then not found at all. GitHub's
// Windows runner is exactly that, and v0.2.10-rc.1 stopped there on
// Get-FileHash. The script hashes and unpacks with .NET instead. This holds on
// every platform, with no PowerShell needed to check it.
func TestInstallPs1NeedsNoScriptModules(t *testing.T) {
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(shipped), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		for _, command := range []string{"Get-FileHash", "Expand-Archive", "Compress-Archive"} {
			if strings.Contains(strings.ToLower(code), strings.ToLower(command)) {
				t.Errorf("install.ps1:%d calls %s, which Windows PowerShell may not find when started from PowerShell 7: %s", i+1, command, code)
			}
		}
	}
}

// Each install script carries the release the site was generated for, and the
// SHA-256 of each of that release's archives it can download, taken from the
// checksums sitegen was given, with no placeholder left over.
func TestInstallScriptsCarryTheReleaseTheyWereGeneratedFor(t *testing.T) {
	dir, _ := generate(t)
	rel := testRelease()
	for name, suffix := range map[string]string{"install.sh": ".tar.gz", "install.ps1": ".zip"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		script := string(b)
		if strings.Contains(script, "@RELEASE") {
			t.Errorf("%s still has a placeholder in it", name)
		}
		if !strings.Contains(script, "'"+rel.Version+"'") && !strings.Contains(script, `"`+rel.Version+`"`) {
			t.Errorf("%s does not name %s as its release", name, rel.Version)
		}
		for _, a := range archives(rel.Version) {
			line := rel.Sums[a] + "  " + a + "\n"
			if has := strings.Contains(script, line); has != strings.HasSuffix(a, suffix) {
				t.Errorf("%s carries %q: %v", name, line, has)
			}
		}
	}
}

// sitegen takes a release's checksums only once the checksums.txt.sig beside
// them is one of its trusted keys' signature of them -- the primary's, or the
// standby's once the build trusts that too. They are what every install from
// the site trusts in place of what it downloads, and checking the signature
// was a step left to whoever ran sitegen.
func TestSitegenChecksTheChecksumsSignature(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	standbyPub, standbyKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	var sums strings.Builder
	for _, a := range archives("v1.2.3") {
		fmt.Fprintf(&sums, "%s  %s\n", strings.Repeat("ab", 32), a)
	}
	signed := []byte(sums.String())
	trusted := []ed25519.PublicKey{pub, standbyPub}
	for _, c := range []struct {
		name      string
		sums, sig []byte
		trusted   []ed25519.PublicKey
		ok        bool
	}{
		{"signed by the primary key", signed, selfupdate.Sign(key, signed), trusted, true},
		{"signed by the standby key", signed, selfupdate.Sign(standbyKey, signed), trusted, true},
		{"with no signature beside it", signed, nil, trusted, false},
		{"signed by another key", signed, selfupdate.Sign(other, signed), trusted, false},
		{"changed after it was signed", []byte(strings.Replace(string(signed), "ab", "cd", 1)), selfupdate.Sign(key, signed), trusted, false},
		{"signed by the standby while it is not trusted", signed, selfupdate.Sign(standbyKey, signed), []ed25519.PublicKey{pub}, false},
		{"read by a build with no release key", signed, selfupdate.Sign(key, signed), nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checksums.txt")
			if err := os.WriteFile(path, c.sums, 0o644); err != nil {
				t.Fatal(err)
			}
			if c.sig != nil {
				if err := os.WriteFile(path+".sig", c.sig, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			rel, err := readRelease("v1.2.3", path, c.trusted)
			if c.ok && (err != nil || len(rel.Sums) != 6) {
				t.Errorf("readRelease gave %v, %v; want the six checksums", rel, err)
			}
			if !c.ok && err == nil {
				t.Errorf("the checksums were taken")
			}
		})
	}
}

// sitegen will not write install scripts without a release and its checksums
// to put in them, nor from checksums that miss one of the release's archives
// or a version that is not a release's tag.
func TestSitegenWantsTheReleaseAndItsChecksums(t *testing.T) {
	for _, c := range []struct{ version, path string }{{"", "checksums.txt"}, {"v1.2.3", ""}} {
		if _, err := readRelease(c.version, c.path, nil); err == nil || !strings.Contains(err.Error(), "-release and -checksums are both required") {
			t.Errorf("readRelease(%q, %q) = %v; want both asked for", c.version, c.path, err)
		}
	}
	var sums strings.Builder
	for _, a := range archives("v1.2.3") {
		fmt.Fprintf(&sums, "%s  %s\r\n", strings.Repeat("ab", 32), a)
	}
	rel, err := parseRelease("v1.2.3", []byte(sums.String()))
	if err != nil || len(rel.Sums) != 6 {
		t.Errorf("a whole checksums.txt, with CRLF, gave %v, %v", rel, err)
	}
	for _, c := range []struct{ version, sums, why string }{
		{"v1.2.4", sums.String(), "another release's checksums"},
		{"v1.2.3", strings.Replace(sums.String(), "_windows_arm64", "_windows_386", 1), "an archive missing"},
		{"v1.2.3", strings.Replace(sums.String(), strings.Repeat("ab", 32)+"  flockdeck_v1.2.3_linux_amd64", "xyz  flockdeck_v1.2.3_linux_amd64", 1), "a checksum that is not one"},
		{`v1.2.3"; rm -rf ~; "`, sums.String(), "a version that is not a tag"},
	} {
		if _, err := parseRelease(c.version, []byte(c.sums)); err == nil {
			t.Errorf("%s was accepted", c.why)
		}
	}
}

// --- moving the pinned install on to whatever is newest --------------------

// updateStubSource stands in for the release the scripts install, in the
// tests that exercise their own step of moving it on to something newer:
// given "update" as its argument it acts as FAKE_UPDATE_MODE says, recording
// that in the file FAKE_UPDATE_MARKER names, and otherwise prints a line so
// a test that ran it by mistake is easy to tell from one that read its bytes.
// It has to be a real, runnable program, unlike installArchive's plain text,
// because these tests run what the script installs rather than only read it.
const updateStubSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		mode := os.Getenv("FAKE_UPDATE_MODE")
		if marker := os.Getenv("FAKE_UPDATE_MARKER"); marker != "" {
			os.WriteFile(marker, []byte(mode), 0o644)
		}
		if mode == "fail" {
			fmt.Fprintln(os.Stderr, "flockdeck update: simulated failure")
			os.Exit(1)
		}
		fmt.Println("flockdeck: updated to vFAKE. It will be in use from the next start.")
		return
	}
	fmt.Println("flockdeck update stub")
}
`

// buildUpdateStub compiles updateStubSource for this machine and returns the
// program's bytes.
func buildUpdateStub(t *testing.T) []byte {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain to build the update stub with")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "stub.go")
	if err := os.WriteFile(src, []byte(updateStubSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "stub")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the update stub: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// generateForUpdate writes the site for a release whose archives carry
// program in place of installArchive's usual plain text.
func generateForUpdate(t *testing.T, program []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := run(dir, defaultRepo, defaultModule, defaultURL, testReleaseWithProgram(program)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return dir
}

// updateCase is one scenario for the scripts' own step, once the pinned
// release above is installed and checked, of asking it to move itself on to
// whatever is newest.
type updateCase struct {
	name     string
	env      map[string]string
	latest   string // dl.flockdeck.ai's latest.json names; "" leaves it unanswered
	ghLatest string // GitHub's "latest release" API names; asked only once dl's is not
	down     bool   // dl.flockdeck.ai (the whole site) is unreachable
	mode     string // FAKE_UPDATE_MODE the installed stub's `update` acts on; "" if it must not run
	says     string // a substring the combined output must contain
}

func updateCases() []updateCase {
	return []updateCase{
		{name: "a newer release named by the site", latest: "v9.9.10", mode: "ok",
			says: "updated to vFAKE"},
		{name: "the same release named by the site", latest: "v9.9.9"},
		{name: "an older release named by the site", latest: "v9.9.8"},
		{name: "nothing named anywhere"},
		{name: "a release chosen by hand", env: map[string]string{"FLOCKDECK_VERSION": "v9.9.9"}, latest: "v9.9.10"},
		{name: "a newer release the site is down for, named by GitHub's API instead",
			down: true, ghLatest: "v9.9.10", mode: "ok", says: "updated to vFAKE"},
		{name: "the update itself fails", latest: "v9.9.10", mode: "fail",
			says: "installed at v9.9.9 and will offer the update when it runs"},
	}
}

// check is what an updateCase asks of one run: the freshly installed
// binary's `update` ran, or did not, exactly as c says, recorded in marker
// rather than read from out, since a script that ran it through a program
// with no console of its own (install.ps1, with no flockdeck-chat.exe to
// prefer) may have nothing of its output to show.
func (c updateCase) check(t *testing.T, marker string, out []byte) {
	t.Helper()
	mode, readErr := os.ReadFile(marker)
	switch ran := readErr == nil; {
	case c.mode == "" && ran:
		t.Errorf("`update` ran (recorded mode %q) when it should not have\n%s", mode, out)
	case c.mode != "" && (!ran || string(mode) != c.mode):
		t.Errorf("`update` did not run as wanted (marker %q, %v)\n%s", mode, readErr, out)
	}
	if c.says != "" && !strings.Contains(string(out), c.says) {
		t.Errorf("the script said %q; want %q in it", out, c.says)
	}
}

// install.sh, once the pinned release above is installed and checked, asks
// it to move itself on to whatever dl.flockdeck.ai's latest.json (or
// GitHub's own idea of the latest release, once that cannot be read) names
// as newer -- unless FLOCKDECK_VERSION or FLOCKDECK_DOWNLOAD said to stay
// put. `flockdeck update` checks the release signature itself, so nothing
// more is checked here. A failure there is not a failure to install: the
// script still exits 0, with the pinned release in place.
func TestInstallShMovesToTheLatestRelease(t *testing.T) {
	sh := installShRunner(t)
	stub := buildUpdateStub(t)
	dir := generateForUpdate(t, stub)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range updateCases() {
		t.Run(c.name, func(t *testing.T) {
			r := newReleases(t)
			r.latest, r.ghLatest, r.down, r.program = c.latest, c.ghLatest, c.down, stub
			script := pointAt(t, string(shipped), r, shAddresses)
			home := t.TempDir()
			path := filepath.Join(home, "install.sh")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(home, "bin")
			marker := filepath.Join(home, "marker")
			cmd := exec.Command(sh, path)
			cmd.Env = append(installEnv(r, c.env), "HOME="+home, "FLOCKDECK_INSTALL_DIR="+bin,
				"FAKE_UPDATE_MODE="+c.mode, "FAKE_UPDATE_MARKER="+marker)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install exited %v; it should exit 0 even when the update step fails\n%s", err, out)
			}
			c.check(t, marker, out)
		})
	}
}

// install.ps1 does the same, asking through flockdeck-chat.exe, which has a
// console to draw `update`'s progress on where flockdeck.exe has none (see
// cli.md's `chat` section).
func TestInstallPs1MovesToTheLatestRelease(t *testing.T) {
	ps := installPs1Runner(t)
	stub := buildUpdateStub(t)
	dir := generateForUpdate(t, stub)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range updateCases() {
		t.Run(c.name, func(t *testing.T) {
			r := newReleases(t)
			r.latest, r.ghLatest, r.down, r.program = c.latest, c.ghLatest, c.down, stub
			script := pointAt(t, string(shipped), r, ps1Addresses)
			home := t.TempDir()
			path := filepath.Join(home, "install.ps1")
			if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(home, "bin")
			marker := filepath.Join(home, "marker")
			cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
			cmd.Env = append(installEnv(r, c.env), "FLOCKDECK_INSTALL_DIR="+bin, "FLOCKDECK_NO_MODIFY_PATH=1",
				"FAKE_UPDATE_MODE="+c.mode, "FAKE_UPDATE_MARKER="+marker)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install exited %v; it should exit 0 even when the update step fails\n%s", err, out)
			}
			c.check(t, marker, out)
		})
	}
}
