// Command release cross-builds Flockdeck for every platform it ships on and
// packages each one the way the updater expects to find it.
//
// Packaging lives in Go rather than in the workflow because the updater has to
// unpack exactly what this produces. A shell pipeline would put the archive
// format in one place and the code that reads it in another, and the two would
// drift the first time a path separator or a compression level changed. It
// also means `make package` on a developer's machine produces the same bytes
// as CI, on Windows, where there is no zip binary to reach for.
//
// Usage:
//
//	go run ./cmd/release -version v1.4.0                 build and package
//	go run ./cmd/release -sign -version v1.4.0           sign it (sign.go)
//	go run ./cmd/release -keygen flockdeck-release.key   make the signing key
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/goreleaser/nfpm/v2"
	_ "github.com/goreleaser/nfpm/v2/deb"
	_ "github.com/goreleaser/nfpm/v2/rpm"
	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// platforms is every platform a release ships for. Windows still
// cross-compiles from any machine, cgo disabled, because its backend
// (internal/appwindow) is WebView2 reached through raw syscalls. Linux and
// macOS build the interface on GTK4/WebKitGTK and Cocoa respectively, both
// cgo, so those five need a matching machine: -platforms picks the subset
// one CI runner builds natively. linux/arm64 tried cross-compiling from the
// same ubuntu-latest runner as linux/amd64 first, but libwebkitgtk-6.0-dev
// and its arm64 copy Conflict with each other in apt, so the two could never
// be installed side by side to link against; it now builds on its own
// native ubuntu-24.04-arm runner instead, the same way every other platform
// here does. The workflow runs a build job per platform and merges what
// each one made (-sums).
var platforms = []struct{ OS, Arch string }{
	{"windows", "amd64"},
	{"windows", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

// selectPlatforms parses -platforms ("linux/amd64,linux/arm64") into the
// matching entries of platforms, or returns every one of them for "".
func selectPlatforms(spec string) ([]struct{ OS, Arch string }, error) {
	if spec == "" {
		return platforms, nil
	}
	var picked []struct{ OS, Arch string }
	for _, s := range strings.Split(spec, ",") {
		s = strings.TrimSpace(s)
		osArch := strings.SplitN(s, "/", 2)
		found := false
		if len(osArch) == 2 {
			for _, p := range platforms {
				if p.OS == osArch[0] && p.Arch == osArch[1] {
					picked = append(picked, p)
					found = true
					break
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("%q is not one of the platforms this command builds (os/arch, such as linux/amd64)", s)
		}
	}
	return picked, nil
}

const binary = "flockdeck"

// chatBinary is the console twin of the Windows build: the program with its
// PE Subsystem set to console. An API agent's pane runs Flockdeck's own chat
// client as its process, and a pane is a pseudo-console, which Windows
// attaches only to console programs: started from the GUI build the pane
// stayed blank. The launcher runs this one instead when it sits beside the
// program.
//
// It is made from the program's own build rather than linked again, so it
// holds exactly the bytes the program writes for itself when its twin is
// missing or stale (selfupdate.EnsureChatTwin): one linked separately differs
// throughout, and every installation would rewrite it at its first start.
const chatBinary = "flockdeck-chat"

// linuxIconSource is the icon install.sh installs for the desktop entry it
// writes on Linux (see its own comment for why that isn't done here yet on
// macOS and Windows too): the same 512x512 PNG appwindow gives the window
// itself, so the icon a user sees in an app menu is the one they see on the
// window's own titlebar/taskbar entry.
const linuxIconSource = "build/appicon.png"

// linuxIconName is where linuxIconSource lands inside a Linux archive.
const linuxIconName = "flockdeck.png"

// nfpmSpec is nfpm's own config for Flockdeck's .deb and .rpm: one spec
// builds both, parsed and built in-process by buildLinuxPackages below, the
// same way writeTarGz and writeZip build the plain archives. See its own
// comments for why the metadata lives there instead of here.
//
//go:embed nfpm.yaml
var nfpmSpec []byte

// linuxPackageFormats are the packaging formats nfpm builds for Linux,
// alongside the tar.gz every platform gets.
var linuxPackageFormats = []string{"deb", "rpm"}

func main() {
	var (
		version  = flag.String("version", "dev", "version to stamp into the binaries and the file names")
		out      = flag.String("out", "dist", "directory to write the archives to")
		keygen   = flag.String("keygen", "", "write a new release signing key to this `file`, print its public key, and stop")
		standby  = flag.Bool("standby", false, "with -keygen, make the standby key instead of the primary: for releaseKeyStandby, kept offline, never in a repository secret")
		sign     = flag.Bool("sign", false, "sign the release already built in -out with the key in "+signingKeyEnv+", and write its manifest and latest.json")
		base     = flag.String("base", selfupdate.Site, "where the download site serves releases, for the URLs in the manifest")
		notes    = flag.String("notes", "", "a `file` of release notes to put in the manifest")
		testKey  = flag.Bool("test-key", false, "with -sign, accept a key the updater does not trust, to try the upload against a store of your own; never for a release")
		platform = flag.String("platforms", "", "comma-separated os/arch pairs to build, such as linux/amd64,linux/arm64 (default: every platform, which needs a cgo toolchain for each on this machine)")
		sumsOnly = flag.Bool("sums", false, "write checksums.txt for the archives already in -out instead of building, for combining a release built across several machines")
	)
	flag.Parse()

	var err error
	switch {
	case *keygen != "":
		err = runKeygen(*keygen, *standby, os.Stdout, os.Stderr)
	case *sign:
		err = runSign(signing{
			version: *version, out: *out, base: *base, notes: *notes,
			key: os.Getenv(signingKeyEnv), anyKey: *testKey, now: time.Now(),
		})
	case *sumsOnly:
		err = runSums(*out)
	default:
		err = run(*version, *out, *platform)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(version, out, platformSpec string) error {
	picked, err := selectPlatforms(platformSpec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := checkExtras(); err != nil {
		return err
	}
	if err := clearOldArchives(out); err != nil {
		return err
	}

	// Sums are collected as the archives are written and spilled at the end,
	// so checksums.txt can never describe an archive that failed to build.
	// Building only some platforms (-platforms) still writes one: -sums
	// recomputes it later from every machine's archives merged together.
	sums := map[string]string{}

	for _, p := range picked {
		names, err := packagePlatform(version, out, p.OS, p.Arch)
		if err != nil {
			return err
		}

		for _, name := range names {
			sum, err := sha256File(filepath.Join(out, name))
			if err != nil {
				return err
			}
			sums[name] = sum
			fmt.Printf("%s  %s\n", sum[:12], name)
		}
	}

	return writeSums(filepath.Join(out, "checksums.txt"), sums)
}

// runSums writes checksums.txt for the archives already sitting in out,
// rather than building anything -- for a release assembled from more than one
// machine's -platforms build, once every archive has been gathered into one
// directory.
func runSums(out string) error {
	entries, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	skip := map[string]bool{"checksums.txt": true, "checksums.txt.sig": true, "manifest.json": true, "manifest.json.sig": true, "latest.json": true}
	sums := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || skip[e.Name()] {
			continue
		}
		sum, err := sha256File(filepath.Join(out, e.Name()))
		if err != nil {
			return err
		}
		sums[e.Name()] = sum
	}
	return writeSums(filepath.Join(out, "checksums.txt"), sums)
}

// packagePlatform builds one platform and writes its archive(s) to out, and
// returns their names. Windows gets the console twin (chatBinary) beside the
// program; Linux gets an icon beside it too, for install.sh to give the
// desktop entry it writes (see its own comment for why macOS and Windows
// don't get one from here yet), and is also packaged as a .deb and a .rpm,
// beside its tar.gz, by buildLinuxPackages.
func packagePlatform(version, out, goos, goarch string) ([]string, error) {
	built := filepath.Join(out, fmt.Sprintf("%s-%s-%s%s", binary, goos, goarch, ext(goos)))
	if err := build(version, goos, goarch, built); err != nil {
		return nil, fmt.Errorf("build %s/%s: %w", goos, goarch, err)
	}
	// files are built for this archive alone and removed once it is written;
	// keep is shipped beside them but is a repository asset used by every
	// platform's archive it applies to, and must survive this function
	// returning so the next platform can still package it too.
	files := []archived{{built, binary + ext(goos)}}
	var keep []archived

	// The twin doubles the Windows download (4.7 MB to 9.4 MB for a build
	// measured here: zip compresses the two near-identical programs apart),
	// and a program that can write its own directory makes the same file at
	// its first start anyway. It is shipped all the same, by decision: an
	// archive unpacked where the program cannot write, such as under Program
	// Files, would otherwise give blank API agent panes with nothing to say
	// why, which is worse than a few megabytes. Do not drop it to save them.
	if goos == "windows" {
		program, err := os.ReadFile(built)
		if err != nil {
			return nil, err
		}
		data, ok := selfupdate.ConsoleTwin(program)
		if !ok {
			return nil, fmt.Errorf("%s/%s: the build is not a GUI-subsystem program to make %s from", goos, goarch, chatBinary)
		}
		twin := filepath.Join(out, fmt.Sprintf("%s-%s-%s%s", chatBinary, goos, goarch, ext(goos)))
		if err := os.WriteFile(twin, data, 0o755); err != nil {
			return nil, err
		}
		files = append(files, archived{twin, chatBinary + ext(goos)})
	}

	if goos == "linux" {
		if _, err := os.Stat(linuxIconSource); err != nil {
			return nil, fmt.Errorf("%s/%s: %s carries the icon install.sh gives the Linux desktop entry, and cannot be missing: %w", goos, goarch, linuxIconSource, err)
		}
		keep = append(keep, archived{linuxIconSource, linuxIconName})
	}

	name := fmt.Sprintf("%s_%s_%s_%s%s", binary, version, goos, goarch, archiveExt(goos))
	archive := filepath.Join(out, name)

	var err error
	if goos == "windows" {
		err = writeZip(archive, files, keep)
	} else {
		err = writeTarGz(archive, files, keep)
	}
	if err != nil {
		return nil, fmt.Errorf("package %s: %w", name, err)
	}
	names := []string{name}

	// A real .deb and .rpm, beside the tar.gz: apt/dnf resolve the GTK4 and
	// WebKitGTK runtime libraries in the archive's stead, and both show
	// Flockdeck in an app menu without install.sh's own best-effort dance
	// (see nfpm.yaml's own comments). Built from the binary still sitting at
	// built, before the loop below removes it.
	if goos == "linux" {
		pkgs, err := buildLinuxPackages(version, out, goarch, built)
		if err != nil {
			return nil, fmt.Errorf("package %s/%s as .deb/.rpm: %w", goos, goarch, err)
		}
		names = append(names, pkgs...)
	}

	// The loose binaries have been folded into the archive and would only
	// confuse a release page that is meant to offer one file per platform.
	// keep is never one of these: it is the repository's own asset, not a
	// scratch build product, and packaging another platform still needs it.
	for _, f := range files {
		if err := os.Remove(f.path); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// buildLinuxPackages builds a .deb and a .rpm from nfpm.yaml, both named and
// placed in out the way the tar.gz beside them is, and returns their names.
// It runs in-process (github.com/goreleaser/nfpm/v2) rather than shelling
// out to dpkg-deb or rpmbuild, so a release still needs no packaging tools
// installed on the machine that builds it -- only a matching cgo toolchain
// to build the binary itself, the same as every other Linux package here.
func buildLinuxPackages(version, out, goarch, builtBinary string) ([]string, error) {
	vars := map[string]string{
		"NFPM_ARCH":    goarch,
		"NFPM_VERSION": strings.TrimPrefix(version, "v"),
		"NFPM_BINARY":  builtBinary,
	}
	cfg, err := nfpm.ParseWithEnvMapping(bytes.NewReader(nfpmSpec), func(k string) string { return vars[k] })
	if err != nil {
		return nil, fmt.Errorf("parse nfpm.yaml: %w", err)
	}

	var names []string
	for _, format := range linuxPackageFormats {
		info, err := cfg.Get(format)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", format, err)
		}
		if err := nfpm.Validate(info); err != nil {
			return nil, fmt.Errorf("%s: %w", format, err)
		}
		packager, err := nfpm.Get(format)
		if err != nil {
			return nil, err
		}

		name := fmt.Sprintf("%s_%s_linux_%s.%s", binary, version, goarch, format)
		f, err := os.Create(filepath.Join(out, name))
		if err != nil {
			return nil, err
		}
		err = packager.Package(info, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, fmt.Errorf("build %s: %w", name, err)
		}
		names = append(names, name)
	}
	return names, nil
}

// clearOldArchives removes what an earlier run wrote to out. Archives of
// another version would otherwise sit beside this run's, missing from its
// checksums.txt, and uploading the directory as it stands would publish them
// with the release; signatures, a manifest and a latest.json left by -sign
// would describe a build that is no longer there. Only the names this command
// writes are touched.
func clearOldArchives(out string) error {
	old, err := filepath.Glob(filepath.Join(out, binary+"_*"))
	if err != nil {
		return err
	}
	for _, n := range []string{"checksums.txt", "checksums.txt.sig", "manifest.json", "manifest.json.sig", "latest.json"} {
		old = append(old, filepath.Join(out, n))
	}
	for _, f := range old {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func ext(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

func archiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// build compiles one platform.
//
// The Windows build asks for the GUI subsystem for the same reason the
// Makefile does: started from a shortcut it should not flash a console window
// behind the interface. A program linked that way is given no console, even
// by a terminal, so the program borrows the terminal's itself (useConsole).
//
// Windows stays cgo-free: its window (internal/appwindow) reaches WebView2
// through raw syscalls, so it is the one target every machine can still
// cross-compile. Linux and macOS draw their window with GTK4/WebKitGTK and
// Cocoa, both cgo, so building them needs the host's own C toolchain for a
// native GOARCH, which is why -platforms restricts each CI runner to what it
// can build natively.
func build(version, goos, goarch, out string) error {
	ldflags := "-s -w -X main.version=" + version
	if goos == "windows" {
		ldflags += " -H=windowsgui"
	}

	cgo := "0"
	if goos == "linux" || goos == "darwin" {
		cgo = "1"
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, ".")
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
		"CGO_ENABLED="+cgo,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// extras are shipped inside every archive, beside the binary.
//
// They are required rather than optional, and that is the point of the check
// below. The front end compiled into the binary includes xterm.js, and the
// Go modules linked into it are MIT, ISC and BSD; every one of those licences
// asks for its copyright notice to travel with the software. A release built
// from a checkout missing these files would distribute all of it with none of
// the notices, and would do it silently, which is exactly what happened for
// every release cut before this check existed.
var extras = []string{"README.md", "LICENSE", "THIRD-PARTY-NOTICES.md"}

// checkExtras refuses to build a release that cannot carry its notices.
//
// It runs before the first compile rather than at the first archive, so the
// answer arrives in a second instead of after six cross-compiles.
func checkExtras() error {
	var missing []string
	for _, e := range extras {
		if _, err := os.Stat(e); err != nil {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("cannot package a release without %s: these carry the licence "+
			"and the notices for everything compiled into the binary, and every archive "+
			"has to contain them (run from the repository root)", strings.Join(missing, " and "))
	}
	return nil
}

// archived is a program built for an archive, and the name it has there.
type archived struct{ path, name string }

func writeZip(archive string, programs, keep []archived) error {
	f, err := os.Create(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, p := range programs {
		if err := zipOne(zw, p.path, p.name, 0o755); err != nil {
			return err
		}
	}
	for _, k := range keep {
		if err := zipOne(zw, k.path, k.name, 0o644); err != nil {
			return err
		}
	}
	for _, e := range extras {
		if err := zipOne(zw, e, filepath.Base(e), 0o644); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

func zipOne(zw *zip.Writer, path, name string, mode os.FileMode) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

func writeTarGz(archive string, programs, keep []archived) error {
	f, err := os.Create(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for _, p := range programs {
		if err := tarOne(tw, p.path, p.name, 0o755); err != nil {
			return err
		}
	}
	for _, k := range keep {
		if err := tarOne(tw, k.path, k.name, 0o644); err != nil {
			return err
		}
	}
	for _, e := range extras {
		if err := tarOne(tw, e, filepath.Base(e), 0o644); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func tarOne(tw *tar.Writer, path, name string, mode int64) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	// The header is written by hand rather than from the file info so an
	// archive does not carry the building machine's modification times and
	// ownership, which would make two builds of the same commit differ.
	if err := tw.WriteHeader(&tar.Header{
		Name:   name,
		Mode:   mode,
		Size:   fi.Size(),
		Format: tar.FormatPAX,
	}); err != nil {
		return err
	}
	_, err = io.Copy(tw, src)
	return err
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeSums writes the file the updater reads to check what it downloaded, in
// the format sha256sum produces, so the same file also works for anyone
// checking a download by hand.
func writeSums(path string, sums map[string]string) error {
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[n], n)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
