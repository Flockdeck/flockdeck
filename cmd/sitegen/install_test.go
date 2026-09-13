package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	tampered bool            // the site serves an archive its checksums do not describe
	forged   string          // a place that serves a tampered archive, and checksums to match it
	asked    []string
	served   []string // the places an archive was served from
}

var archiveName = regexp.MustCompile(`^flockdeck_(v[^_]+)_([a-z]+)_([a-z0-9]+)\.(tar\.gz|zip)$`)

// testRelease is the release the tests generate the site for: v9.9.9, which
// the site and GitHub both have, with the checksums of its archives as they
// serve them.
func testRelease() release {
	rel := release{Version: "v9.9.9", Sums: map[string]string{}}
	for _, name := range archives(rel.Version) {
		sum := sha256.Sum256(installArchive(name))
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
	onSite, down, tampered, forged := r.onSite, r.down, r.tampered, r.forged
	r.mu.Unlock()

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
			sum := sha256.Sum256(installArchive(described))
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
	w.Write(installArchive(file))
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
	var buf bytes.Buffer
	body := []byte("flockdeck " + archiveName.FindStringSubmatch(name)[1])
	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("flockdeck.exe")
		w.Write(body)
		zw.Close()
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "flockdeck", Mode: 0o755, Size: int64(len(body)), Format: tar.FormatPAX})
	tw.Write(body)
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
		`DL="https://dl.flockdeck.ai"`: `DL="%s/dl"`,
		`GITHUB="https://github.com"`:  `GITHUB="%s/gh"`,
	}
	ps1Addresses = map[string]string{
		`$dl = 'https://dl.flockdeck.ai'`: `$dl = '%s/dl'`,
		`$github = 'https://github.com'`:  `$github = '%s/gh'`,
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
			c.check(t, r, filepath.Join(bin, "flockdeck"), out, err)
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
			want := filepath.Join(home, ".local", "bin", "flockdeck")
			if installDir != "" {
				cmd.Env = append(cmd.Env, "FLOCKDECK_INSTALL_DIR="+installDir)
				want = filepath.Join(home, "bin", "flockdeck")
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if _, err := os.Stat(want); err != nil {
				t.Errorf("nothing installed at %s: %v\n%s", want, err, out)
			}
			if !strings.Contains(string(out), "start it with: flockdeck\n") ||
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
	// Every program the script uses, but neither of the two that download.
	tools := filepath.Join(home, "tools")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uname", "sysctl", "mktemp", "rm", "sed", "head", "cut", "awk",
		"tar", "sha256sum", "shasum", "mkdir", "cp", "chmod", "mv"} {
		if p, err := exec.LookPath(name); err == nil {
			if err := os.Symlink(p, filepath.Join(tools, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
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

// With another flockdeck first on PATH, the script says to start the one it
// installed by its path, and a directory with a space in it split that path
// into two words when pasted.
func TestInstallShQuotesThePathItSaysToStart(t *testing.T) {
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

// install.ps1 does as install.sh does. It runs where Windows PowerShell does,
// installing into a temporary directory and leaving PATH and the Start menu
// alone.
func TestInstallPs1DownloadsFromTheSite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 reads the machine's architecture from the Windows registry")
	}
	ps, err := exec.LookPath("powershell")
	if err != nil {
		t.Skip("no Windows PowerShell to run it with")
	}
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

// sitegen will not write install scripts without a release and its checksums
// to put in them, nor from checksums that miss one of the release's archives
// or a version that is not a release's tag.
func TestSitegenWantsTheReleaseAndItsChecksums(t *testing.T) {
	for _, c := range []struct{ version, path string }{{"", "checksums.txt"}, {"v1.2.3", ""}} {
		if _, err := readRelease(c.version, c.path); err == nil || !strings.Contains(err.Error(), "-release and -checksums are both required") {
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
