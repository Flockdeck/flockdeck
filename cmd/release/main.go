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
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
// writes on Linux, and that buildDarwinBundle turns into Contents/Resources/
// AppIcon.icns for the macOS one: the same 512x512 PNG appwindow gives the
// window itself, so the icon a user sees in an app menu or the Dock is the
// one they see on the window's own titlebar/taskbar entry.
const linuxIconSource = "build/appicon.png"

// linuxIconName is where linuxIconSource lands inside a Linux archive.
const linuxIconName = "flockdeck.png"

// darwinBundleName is the .app every macOS archive carries, and what
// install.sh copies into ~/Applications.
const darwinBundleName = "Flockdeck.app"

// darwinBundleID is Flockdeck's CFBundleIdentifier: the reverse-DNS form of
// the domain it ships from (flockdeck.ai), which is the convention macOS
// expects and what Launch Services keys this app's identity off.
const darwinBundleID = "ai.flockdeck.app"

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

// packagePlatform builds one platform and writes its archive to out, and
// returns the archive's name. Windows gets the console twin (chatBinary)
// beside the program; Linux gets an icon beside it too, for install.sh to
// give the desktop entry it writes; macOS gets a real, ad-hoc-signed
// Flockdeck.app instead of the bare binary every other platform ships,
// carrying its own copy of the icon (see buildDarwinBundle).
func packagePlatform(version, out, goos, goarch string) (string, error) {
	built := filepath.Join(out, fmt.Sprintf("%s-%s-%s%s", binary, goos, goarch, ext(goos)))
	if err := build(version, goos, goarch, built); err != nil {
		return "", fmt.Errorf("build %s/%s: %w", goos, goarch, err)
	}
	// files are built for this archive alone and removed once it is written;
	// keep is shipped beside them but is a repository asset used by every
	// platform's archive it applies to, and must survive this function
	// returning so the next platform can still package it too.
	files := []archived{{built, binary + ext(goos)}}
	var keep []archived
	var trees []archivedTree

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

	if goos == "linux" {
		if _, err := os.Stat(linuxIconSource); err != nil {
			return "", fmt.Errorf("%s/%s: %s carries the icon install.sh gives the Linux desktop entry, and cannot be missing: %w", goos, goarch, linuxIconSource, err)
		}
		keep = append(keep, archived{linuxIconSource, linuxIconName})
	}

	if goos == "darwin" {
		bundle, err := buildDarwinBundle(built, version)
		if err != nil {
			return "", fmt.Errorf("%s/%s: %w", goos, goarch, err)
		}
		defer os.RemoveAll(filepath.Dir(bundle))
		trees = append(trees, archivedTree{bundle, darwinBundleName})
	}

	name := fmt.Sprintf("%s_%s_%s_%s%s", binary, version, goos, goarch, archiveExt(goos))
	archive := filepath.Join(out, name)

	// The bundle already carries the binary at Contents/MacOS/flockdeck; a
	// second, loose copy at the archive's root, as every other platform
	// gets, would only double it with the same bytes, and nothing would
	// unpack it there.
	root := files
	if goos == "darwin" {
		root = nil
	}

	var err error
	if goos == "windows" {
		err = writeZip(archive, root, keep)
	} else {
		err = writeTarGz(archive, root, keep, trees...)
	}
	if err != nil {
		return "", fmt.Errorf("package %s: %w", name, err)
	}

	// The loose binaries have been folded into the archive and would only
	// confuse a release page that is meant to offer one file per platform.
	// keep is never one of these: it is the repository's own asset, not a
	// scratch build product, and packaging another platform still needs it.
	for _, f := range files {
		if err := os.Remove(f.path); err != nil {
			return "", err
		}
	}
	return name, nil
}

// buildDarwinBundle assembles Flockdeck.app around built, the binary
// packagePlatform already compiled for this goarch, in a scratch directory
// of its own, ad-hoc code-signs it, and returns the bundle's path. The
// caller removes the scratch directory (the bundle's parent) once it has
// been archived.
//
// The bundle is what gives macOS a real application: without one, the OS has
// no Info.plist to read a name or icon from, so a downloaded binary is
// neither Dock/Spotlight/Launchpad-visible nor double-clickable as anything
// but a loose Unix executable, which is exactly the "do I have to use a
// terminal?" confusion this exists to fix.
func buildDarwinBundle(built, version string) (string, error) {
	scratch, err := os.MkdirTemp("", "flockdeck-bundle-*")
	if err != nil {
		return "", err
	}
	bundle := filepath.Join(scratch, darwinBundleName)
	macosDir := filepath.Join(bundle, "Contents", "MacOS")
	resourcesDir := filepath.Join(bundle, "Contents", "Resources")
	if err := os.MkdirAll(macosDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		return "", err
	}

	program, err := os.ReadFile(built)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(macosDir, binary), program, 0o755); err != nil {
		return "", err
	}

	if _, err := os.Stat(linuxIconSource); err != nil {
		return "", fmt.Errorf("%s carries the icon Contents/Resources/AppIcon.icns is made from, and cannot be missing: %w", linuxIconSource, err)
	}
	if err := buildIcns(linuxIconSource, filepath.Join(resourcesDir, "AppIcon.icns")); err != nil {
		return "", fmt.Errorf("AppIcon.icns: %w", err)
	}

	plist := darwinInfoPlist(version)
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		return "", err
	}

	if err := codesignAdHoc(bundle); err != nil {
		return "", fmt.Errorf("codesign: %w", err)
	}
	return bundle, nil
}

// darwinIconSizes is every size a .iconset holds, named as iconutil requires.
// The source PNG (linuxIconSource) is 512x512, so the 1024x1024 variant
// iconutil also accepts ("icon_512x512@2x.png") is left out rather than
// upscaled to it: a blurrier icon at that size would be worse than the next
// one down, which macOS already scales for a Retina display fine.
var darwinIconSizes = []struct {
	name string
	px   int
}{
	{"icon_16x16.png", 16},
	{"icon_16x16@2x.png", 32},
	{"icon_32x32.png", 32},
	{"icon_32x32@2x.png", 64},
	{"icon_128x128.png", 128},
	{"icon_128x128@2x.png", 256},
	{"icon_256x256.png", 256},
	{"icon_256x256@2x.png", 512},
	{"icon_512x512.png", 512},
}

// buildIcns makes dst, an .icns, from src, the repository's 512x512 source
// icon, by resizing it with sips into every size a .iconset holds and
// handing that to iconutil -- the same two tools Xcode's own build uses for
// an app icon, and both part of macOS itself; neither needs the Xcode
// Command Line Tools installed.
func buildIcns(src, dst string) error {
	scratch, err := os.MkdirTemp("", "flockdeck-iconset-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	iconset := filepath.Join(scratch, "AppIcon.iconset")
	if err := os.MkdirAll(iconset, 0o755); err != nil {
		return err
	}
	for _, s := range darwinIconSizes {
		out := filepath.Join(iconset, s.name)
		cmd := exec.Command("sips", "-z", strconv.Itoa(s.px), strconv.Itoa(s.px), src, "--out", out)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("sips %s: %w", s.name, err)
		}
	}

	cmd := exec.Command("iconutil", "-c", "icns", iconset, "-o", dst)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// codesignAdHoc signs bundle with an ad-hoc identity: --sign - is codesign's
// own syntax for one, needing no Apple Developer account or certificate.
// Gatekeeper still shows a first-run "Apple could not verify this app" prompt
// for a bundle signed this way rather than notarized, but it is one the user
// can get past themselves, from System Settings > Privacy & Security > Open
// Anyway. A fully unsigned .app is refused outright instead ("is damaged and
// can't be opened"), with no prompt and nothing the user can do about it --
// the gap this exists to close. --deep signs everything under Contents,
// which here is only the one executable and the icon, so it does not need
// signing on its own first.
func codesignAdHoc(bundle string) error {
	cmd := exec.Command("codesign", "--force", "--deep", "--sign", "-", bundle)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// darwinInfoPlist is Contents/Info.plist. LSUIElement is deliberately absent:
// Flockdeck is a normal windowed app (one main window; see
// internal/appwindow), not a menu-bar-only accessory, and Wails' own default
// activation policy already gives it a Dock icon and an app-switcher entry --
// this only has to not turn that off. NSHighResolutionCapable is set so the
// window is not upscaled blurry on a Retina display, as every other modern
// Mac app is.
func darwinInfoPlist(version string) string {
	v := strings.TrimPrefix(version, "v")
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + darwinBundleID + `</string>
	<key>CFBundleName</key>
	<string>Flockdeck</string>
	<key>CFBundleDisplayName</key>
	<string>Flockdeck</string>
	<key>CFBundleExecutable</key>
	<string>` + binary + `</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>` + v + `</string>
	<key>CFBundleVersion</key>
	<string>` + v + `</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
`
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

// archivedTree is a directory copied into a tar.gz archive with its own
// structure intact, under prefix -- the macOS .app bundle, which is a
// directory rather than the single file archived describes. Only
// writeTarGz takes these; the Windows archive never carries a directory.
type archivedTree struct{ root, prefix string }

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

func writeTarGz(archive string, programs, keep []archived, trees ...archivedTree) error {
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
	for _, t := range trees {
		if err := tarTree(tw, t.root, t.prefix); err != nil {
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

// tarTree walks root and writes every entry under it into tw, named as
// prefix would have it inside the archive (such as
// "Flockdeck.app/Contents/MacOS/flockdeck"). A file already executable on
// disk -- the bundle's own binary, made that way by buildDarwinBundle -- is
// archived executable; everything else, Info.plist and the icon, is not.
func tarTree(tw *tar.Writer, root, prefix string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := prefix
		if rel != "." {
			name = prefix + "/" + filepath.ToSlash(rel)
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:     name + "/",
				Typeflag: tar.TypeDir,
				Mode:     0o755,
				Format:   tar.FormatPAX,
			})
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if fi.Mode()&0o111 != 0 {
			mode = 0o755
		}
		return tarOne(tw, path, name, mode)
	})
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
