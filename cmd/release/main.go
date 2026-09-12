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
//	go run ./cmd/release -version v1.4.0
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
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

	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// platforms is what a release is built for. It matches the Makefile's list,
// and every one of them cross-compiles from any one machine because the
// application is built with cgo disabled.
var platforms = []struct{ OS, Arch string }{
	{"windows", "amd64"},
	{"windows", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
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

func main() {
	var (
		version = flag.String("version", "dev", "version to stamp into the binaries and the file names")
		out     = flag.String("out", "dist", "directory to write the archives to")
	)
	flag.Parse()

	if err := run(*version, *out); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(version, out string) error {
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
	sums := map[string]string{}

	for _, p := range platforms {
		name, err := packagePlatform(version, out, p.OS, p.Arch)
		if err != nil {
			return err
		}

		sum, err := sha256File(filepath.Join(out, name))
		if err != nil {
			return err
		}
		sums[name] = sum
		fmt.Printf("%s  %s\n", sum[:12], name)
	}

	return writeSums(filepath.Join(out, "checksums.txt"), sums)
}

// packagePlatform builds one platform and writes its archive to out, and
// returns the archive's name. Windows gets the console twin (chatBinary)
// beside the program.
func packagePlatform(version, out, goos, goarch string) (string, error) {
	built := filepath.Join(out, fmt.Sprintf("%s-%s-%s%s", binary, goos, goarch, ext(goos)))
	if err := build(version, goos, goarch, built); err != nil {
		return "", fmt.Errorf("build %s/%s: %w", goos, goarch, err)
	}
	files := []archived{{built, binary + ext(goos)}}

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
			return "", err
		}
		data, ok := selfupdate.ConsoleTwin(program)
		if !ok {
			return "", fmt.Errorf("%s/%s: the build is not a GUI-subsystem program to make %s from", goos, goarch, chatBinary)
		}
		twin := filepath.Join(out, fmt.Sprintf("%s-%s-%s%s", chatBinary, goos, goarch, ext(goos)))
		if err := os.WriteFile(twin, data, 0o755); err != nil {
			return "", err
		}
		files = append(files, archived{twin, chatBinary + ext(goos)})
	}

	name := fmt.Sprintf("%s_%s_%s_%s%s", binary, version, goos, goarch, archiveExt(goos))
	archive := filepath.Join(out, name)

	var err error
	if goos == "windows" {
		err = writeZip(archive, files)
	} else {
		err = writeTarGz(archive, files)
	}
	if err != nil {
		return "", fmt.Errorf("package %s: %w", name, err)
	}

	// The loose binaries have been folded into the archive and would only
	// confuse a release page that is meant to offer one file per platform.
	for _, f := range files {
		if err := os.Remove(f.path); err != nil {
			return "", err
		}
	}
	return name, nil
}

// clearOldArchives removes what an earlier run wrote to out. Archives of
// another version would otherwise sit beside this run's, missing from its
// checksums.txt, and uploading the directory as it stands would publish them
// with the release. Only the names this command writes are touched.
func clearOldArchives(out string) error {
	old, err := filepath.Glob(filepath.Join(out, binary+"_*"))
	if err != nil {
		return err
	}
	for _, f := range append(old, filepath.Join(out, "checksums.txt")) {
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
func build(version, goos, goarch, out string) error {
	ldflags := "-s -w -X main.version=" + version
	if goos == "windows" {
		ldflags += " -H=windowsgui"
	}

	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, ".")
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
		"CGO_ENABLED=0",
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

func writeZip(archive string, programs []archived) error {
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

func writeTarGz(archive string, programs []archived) error {
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
