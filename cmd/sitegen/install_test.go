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
	"strings"
	"sync"
	"testing"
)

// releases stands in for everywhere the install scripts download from, under
// one server: the site at /dl, GitHub's downloads at /gh, GitHub's API at /api
// and a mirror of somebody's own at /mirror. Every archive holds a program
// saying which of them it came from, so a test can tell where an installation
// was fetched.
type releases struct {
	srv *httptest.Server

	mu       sync.Mutex
	onSite   map[string]bool // versions the site has; GitHub has every one
	latest   string          // what the site's latest.json names
	down     bool            // the site drops every connection
	tampered bool            // the site serves an archive its checksums do not describe
	asked    []string
}

var archiveName = regexp.MustCompile(`^flockdeck_(v[^_]+)_([a-z]+)_([a-z0-9]+)\.(tar\.gz|zip)$`)

func newReleases(t *testing.T) *releases {
	t.Helper()
	r := &releases{onSite: map[string]bool{"v9.9.9": true}, latest: "v9.9.9"}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *releases) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.asked = append(r.asked, req.URL.Path)
	onSite, latest, down, tampered := r.onSite, r.latest, r.down, r.tampered
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
	case place == "api" && rest == "repos/jmwri/flockdeck/releases/latest":
		fmt.Fprint(w, `{"tag_name": "v9.9.9", "draft": false}`)
		return
	case (place == "dl" || place == "mirror") && rest == "latest.json":
		// As cmd/release writes it: the version, and nothing else.
		fmt.Fprintf(w, "{\"version\":%q}\n", latest)
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
	if file == "checksums.txt" {
		// Every platform, so that whatever machine runs the test finds its own.
		for _, p := range []string{"linux_amd64.tar.gz", "linux_arm64.tar.gz", "darwin_amd64.tar.gz", "darwin_arm64.tar.gz", "windows_amd64.zip", "windows_arm64.zip"} {
			name := "flockdeck_" + version + "_" + p
			sum := sha256.Sum256(installArchive(name, place))
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		}
		return
	}
	if !archiveName.MatchString(file) {
		http.NotFound(w, req)
		return
	}
	if place == "dl" && tampered {
		file = strings.Replace(file, version, version+"-evil", 1)
	}
	w.Write(installArchive(file, place))
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

// installArchive is the archive of that name as a place serves it: the same
// bytes every time, holding a program that names the place.
func installArchive(name, place string) []byte {
	var buf bytes.Buffer
	body := []byte("flockdeck " + archiveName.FindStringSubmatch(name)[1] + " from " + place)
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
		{name: "from the site", from: "dl", version: "v9.9.9", never: []string{"gh", "api"}},
		{name: "the site out of reach", setup: func(r *releases) { r.down = true },
			from: "gh", version: "v9.9.9", says: "downloading from GitHub instead"},
		{name: "the site naming no release", setup: func(r *releases) { r.latest = "<html>" },
			from: "gh", version: "v9.9.9", says: "downloading from GitHub instead"},
		{name: "a release older than the site", env: map[string]string{"FLOCKDECK_VERSION": "0.1.0"},
			from: "gh", version: "v0.1.0", says: "downloading it from GitHub instead", never: []string{"api"}},
		{name: "a mirror", env: map[string]string{"FLOCKDECK_DOWNLOAD": "%s/mirror/"},
			from: "mirror", version: "v9.9.9", never: []string{"dl", "gh", "api"}},
		{name: "a tampered archive on the site", setup: func(r *releases) { r.tampered = true },
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
	} else if err != nil || string(got) != "flockdeck "+c.version+" from "+c.from {
		t.Errorf("installed %q (%v); want %s from %s\n%s", got, err, c.version, c.from, out)
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

// install.sh finds the latest release in the site's latest.json, downloads it
// and its checksums from the site, and goes to GitHub for either when the site
// cannot give it; a mirror given by hand is the only place asked; and an
// archive that does not match the site's checksums is never installed.
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
			script := pointAt(t, string(shipped), r, map[string]string{
				`DL="https://dl.flockdeck.ai"`:        `DL="%s/dl"`,
				`GITHUB="https://github.com"`:         `GITHUB="%s/gh"`,
				`GITHUB_API="https://api.github.com"`: `GITHUB_API="%s/api"`,
			})
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

// A directory given with a slash on the end is the same directory. Compared
// as typed, one on PATH was "not on your PATH", and the flockdeck just put
// there was "not this one".
func TestInstallShTakesADirectoryWithASlashOnTheEnd(t *testing.T) {
	sh := installShRunner(t)
	dir, _ := generate(t)
	shipped, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	r := newReleases(t)
	script := pointAt(t, string(shipped), r, map[string]string{
		`DL="https://dl.flockdeck.ai"`:        `DL="%s/dl"`,
		`GITHUB="https://github.com"`:         `GITHUB="%s/gh"`,
		`GITHUB_API="https://api.github.com"`: `GITHUB_API="%s/api"`,
	})
	home := t.TempDir()
	path := filepath.Join(home, "install.sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	cmd := exec.Command(sh, path)
	cmd.Env = append(installEnv(r, nil), "HOME="+home, "FLOCKDECK_INSTALL_DIR="+bin+"/",
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "start it with: flockdeck\n") ||
		strings.Contains(string(out), "not on your PATH") || strings.Contains(string(out), "not this one") {
		t.Errorf("installing into %s/ with %s on PATH said:\n%s", bin, bin, out)
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
	script := pointAt(t, string(shipped), r, map[string]string{
		`DL="https://dl.flockdeck.ai"`:        `DL="%s/dl"`,
		`GITHUB="https://github.com"`:         `GITHUB="%s/gh"`,
		`GITHUB_API="https://api.github.com"`: `GITHUB_API="%s/api"`,
	})
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
	for _, p := range []string{"dl", "gh", "api"} {
		if asked := r.askedOf(p); len(asked) > 0 {
			t.Errorf("asked %v of %s", asked, p)
		}
	}
}

// install.ps1 does as install.sh does, reading latest.json with
// ConvertFrom-Json. It runs where Windows PowerShell does, installing into a
// temporary directory and leaving PATH and the Start menu alone.
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
			script := pointAt(t, string(shipped), r, map[string]string{
				`$dl = 'https://dl.flockdeck.ai'`:       `$dl = '%s/dl'`,
				`$github = 'https://github.com'`:        `$github = '%s/gh'`,
				`$githubApi = 'https://api.github.com'`: `$githubApi = '%s/api'`,
			})
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
