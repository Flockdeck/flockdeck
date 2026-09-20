package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"debug/pe"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blakesmith/ar"
	"github.com/jmwri/flockdeck/internal/selfupdate"
)

// entry is one file read back out of an archive.
type entry struct {
	regular bool
	mode    os.FileMode
	body    string
}

// The updater takes the binary out of an archive only when it is a regular
// file named as it expects, and every archive has to carry the licence and the
// notices. Both archive formats are read back here the way the updater reads
// them, since the tar one is never unpacked on the machine that builds it when
// that machine is Windows.
func TestArchivesHoldWhatTheUpdaterAndTheLicencesNeed(t *testing.T) {
	t.Chdir(filepath.Join("..", "..")) // where the extras are found
	dir := t.TempDir()
	built := filepath.Join(dir, "built")
	if err := os.WriteFile(built, []byte("the program"), 0o755); err != nil {
		t.Fatal(err)
	}

	tgz := filepath.Join(dir, "flockdeck.tar.gz")
	if err := writeTarGz(tgz, []archived{{built, "flockdeck"}}, nil); err != nil {
		t.Fatal(err)
	}
	checkArchive(t, "tar.gz", readTarGz(t, tgz), "flockdeck")

	// The Windows archive carries the console twin an API agent's pane runs.
	zp := filepath.Join(dir, "flockdeck.zip")
	if err := writeZip(zp, []archived{{built, "flockdeck.exe"}, {built, "flockdeck-chat.exe"}}, nil); err != nil {
		t.Fatal(err)
	}
	checkArchive(t, "zip", readZip(t, zp), "flockdeck.exe", "flockdeck-chat.exe")
}

// keep is shipped beside an archive's programs, at 0o644 like the licence
// extras rather than 0o755 like a program, and is not removed the way a
// scratch build product is: it is the repository's own asset (see
// packagePlatform's own comment on why files and keep are kept apart).
func TestKeepIsShippedButNotRemovable(t *testing.T) {
	t.Chdir(filepath.Join("..", "..")) // where the extras are found
	dir := t.TempDir()
	built := filepath.Join(dir, "built")
	if err := os.WriteFile(built, []byte("the program"), 0o755); err != nil {
		t.Fatal(err)
	}
	icon := filepath.Join(dir, "icon.png")
	if err := os.WriteFile(icon, []byte("not really a png"), 0o644); err != nil {
		t.Fatal(err)
	}

	tgz := filepath.Join(dir, "flockdeck.tar.gz")
	if err := writeTarGz(tgz, []archived{{built, "flockdeck"}}, []archived{{icon, "flockdeck.png"}}); err != nil {
		t.Fatal(err)
	}
	got := readTarGz(t, tgz)
	entry, ok := got["flockdeck.png"]
	if !ok || entry.body != "not really a png" || entry.mode&0o111 != 0 {
		t.Errorf("tar.gz: flockdeck.png = %+v, %v; want the icon's bytes at 0o644", entry, ok)
	}
	if _, err := os.Stat(icon); err != nil {
		t.Errorf("the icon was removed from %s, but it is a repository asset, not a build product: %v", dir, err)
	}
}

// A pane is a console, and Windows gives one only to a console program, so
// the Windows archive carries, beside the GUI program that opens the window,
// a console twin for an API agent's pane to run. The archive a release
// publishes is made here and read back, the programs' PE subsystems with it.
func TestWindowsArchiveCarriesAConsoleTwin(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program twice")
	}
	t.Chdir(filepath.Join("..", ".."))
	out := t.TempDir()
	names, err := packagePlatform("v9.9.9", out, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("packagePlatform windows/amd64 = %v, want exactly one archive", names)
	}

	zr, err := zip.OpenReader(filepath.Join(out, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	want := map[string]uint16{
		binary + ".exe":     pe.IMAGE_SUBSYSTEM_WINDOWS_GUI,
		chatBinary + ".exe": pe.IMAGE_SUBSYSTEM_WINDOWS_CUI,
	}
	contents := map[string][]byte{}
	for _, f := range zr.File {
		sub, ok := want[f.Name]
		if !ok {
			continue
		}
		delete(want, f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[f.Name] = data
		pf, err := pe.NewFile(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		oh, ok := pf.OptionalHeader.(*pe.OptionalHeader64)
		switch {
		case !ok:
			t.Errorf("%s is not a 64-bit Windows program", f.Name)
		case oh.Subsystem != sub:
			t.Errorf("%s is linked for subsystem %d, want %d", f.Name, oh.Subsystem, sub)
		}
	}
	for n := range want {
		t.Errorf("the Windows archive has no %s", n)
	}
	// Exactly what the program makes of itself, so an installation never
	// rewrites the twin it was shipped with.
	if made, ok := selfupdate.ConsoleTwin(contents[binary+".exe"]); !ok || !bytes.Equal(made, contents[chatBinary+".exe"]) {
		t.Error("the shipped twin is not the program with only its subsystem set to console")
	}
	// The loose programs went into the archive and are gone.
	if entries, _ := os.ReadDir(out); len(entries) != 1 {
		t.Errorf("beside the archive: %v, want nothing else", entries)
	}
}

// The Linux archive carries the icon install.sh gives the desktop entry it
// writes, and packaging it leaves the repository's own copy alone: only
// linux/amd64 packages natively cross-compile CGO_ENABLED=1 for (see
// packagePlatform's own doc comment), so this only runs where the host
// actually is linux/amd64, the same way test.yml's own build jobs are split
// one per platform rather than cross-compiled from a single runner.
func TestLinuxArchiveCarriesAnIcon(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("packaging linux/amd64 needs a matching machine's own cgo toolchain (GTK4/WebKitGTK)")
	}
	t.Chdir(filepath.Join("..", ".."))
	out := t.TempDir()
	names, err := packagePlatform("v9.9.9", out, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	name := names[0]
	if !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("packagePlatform linux/amd64 = %v, want the tar.gz first", names)
	}

	want, err := os.ReadFile(linuxIconSource)
	if err != nil {
		t.Fatal(err)
	}
	got := readTarGz(t, filepath.Join(out, name))
	entry, ok := got[linuxIconName]
	if !ok || entry.body != string(want) {
		t.Errorf("the archive's %s does not match %s", linuxIconName, linuxIconSource)
	}
	if entry.mode&0o111 != 0 {
		t.Errorf("%s is mode %v, an icon does not need to be executable", linuxIconName, entry.mode)
	}
	if _, err := os.Stat(linuxIconSource); err != nil {
		t.Errorf("%s was removed by packaging, but it is a repository asset: %v", linuxIconSource, err)
	}
}

// buildLinuxPackages needs no cgo toolchain -- it packages whatever binary it
// is given, built or not -- so unlike packagePlatform's own Linux test this
// runs on every platform's own build of this test suite, the way CI already
// does for the rest of cmd/release.
func TestLinuxPackagesCarryTheBinaryDesktopFileAndIcon(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	out := t.TempDir()
	built := filepath.Join(t.TempDir(), "built")
	if err := os.WriteFile(built, []byte("the program"), 0o755); err != nil {
		t.Fatal(err)
	}

	names, err := buildLinuxPackages("v1.2.3", out, "amd64", built)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := map[string]bool{"flockdeck_v1.2.3_linux_amd64.deb": true, "flockdeck_v1.2.3_linux_amd64.rpm": true}
	for _, n := range names {
		if !wantNames[n] {
			t.Errorf("buildLinuxPackages made %s, not one of %v", n, wantNames)
		}
		delete(wantNames, n)
	}
	for n := range wantNames {
		t.Errorf("buildLinuxPackages did not make %s", n)
	}

	icon, err := os.ReadFile(linuxIconSource)
	if err != nil {
		t.Fatal(err)
	}
	desktop, err := os.ReadFile("build/linux/flockdeck.desktop")
	if err != nil {
		t.Fatal(err)
	}

	deb := readDebDataTarGz(t, filepath.Join(out, "flockdeck_v1.2.3_linux_amd64.deb"))
	wantDeb := map[string]entry{
		"./usr/bin/flockdeck":                                  {regular: true, body: "the program"},
		"./usr/share/applications/flockdeck.desktop":           {regular: true, body: string(desktop)},
		"./usr/share/icons/hicolor/512x512/apps/flockdeck.png": {regular: true, body: string(icon)},
		"./usr/share/doc/flockdeck/LICENSE":                    {regular: true},
		"./usr/share/doc/flockdeck/THIRD-PARTY-NOTICES.md":     {regular: true},
	}
	for name, want := range wantDeb {
		got, ok := deb[name]
		switch {
		case !ok:
			t.Errorf(".deb: no %s in %v", name, deb)
		case want.body != "" && got.body != want.body:
			t.Errorf(".deb: %s = %q, want %q", name, got.body, want.body)
		}
	}
	if bin, ok := deb["./usr/bin/flockdeck"]; ok && bin.mode&0o111 == 0 {
		t.Errorf(".deb: /usr/bin/flockdeck is mode %v, which will not run", bin.mode)
	}

	// A full RPM payload is cpio inside a custom header format the standard
	// library has no reader for; the magic number at least confirms nfpm
	// wrote a real RPM and not, say, an error message. Actual installability
	// is checked by hand in a container -- see the release worktree's own
	// notes -- since that is what an RPM's format is for in the first place.
	rpm, err := os.ReadFile(filepath.Join(out, "flockdeck_v1.2.3_linux_amd64.rpm"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rpm) < 4 || rpm[0] != 0xed || rpm[1] != 0xab || rpm[2] != 0xee || rpm[3] != 0xdb {
		t.Errorf(".rpm does not start with the RPM lead magic bytes: %x", rpm[:min(4, len(rpm))])
	}
}

// readDebDataTarGz unpacks a .deb (an ar archive of debian-binary,
// control.tar.gz and data.tar.gz) and reads back data.tar.gz, the part that
// actually lands on the filesystem an install unpacks it onto.
func readDebDataTarGz(t *testing.T, path string) map[string]entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rd := ar.NewReader(f)
	for {
		h, err := rd.Next()
		if err == io.EOF {
			t.Fatalf("%s has no data.tar.gz", path)
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(h.Name) == "data.tar.gz" {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, rd); err != nil {
				t.Fatal(err)
			}
			gz, err := gzip.NewReader(&buf)
			if err != nil {
				t.Fatal(err)
			}
			tr := tar.NewReader(gz)
			out := map[string]entry{}
			for {
				th, err := tr.Next()
				if err == io.EOF {
					return out
				}
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				out[th.Name] = entry{th.Typeflag == tar.TypeReg, os.FileMode(th.Mode).Perm(), string(body)}
			}
		}
	}
}

// The macOS archive carries a real, ad-hoc-signed Flockdeck.app rather than a
// bare binary: without one there is no Info.plist for the OS to read a name
// or icon from, so a downloaded binary is neither Dock/Spotlight/Launchpad-
// visible nor double-clickable as anything but a loose Unix executable.
// Building one needs sips, iconutil and codesign, all part of macOS itself,
// so this only runs where the host actually is a Mac, the same way
// TestLinuxArchiveCarriesAnIcon is native-only.
func TestDarwinArchiveIsASignedAppBundle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("packaging darwin needs a Mac's own sips, iconutil and codesign")
	}
	t.Chdir(filepath.Join("..", ".."))
	out := t.TempDir()
	names, err := packagePlatform("v9.9.9", out, "darwin", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("packagePlatform darwin/amd64 = %v, want exactly one archive", names)
	}
	name := names[0]

	got := readTarGz(t, filepath.Join(out, name))
	bin, ok := got["Flockdeck.app/Contents/MacOS/flockdeck"]
	if !ok || !bin.regular || bin.mode&0o111 == 0 {
		t.Errorf("Flockdeck.app/Contents/MacOS/flockdeck = %+v, %v; want an executable regular file", bin, ok)
	}
	if icon, ok := got["Flockdeck.app/Contents/Resources/AppIcon.icns"]; !ok || icon.body == "" {
		t.Errorf("Flockdeck.app/Contents/Resources/AppIcon.icns = %+v, %v; want a non-empty file", icon, ok)
	}
	plist, ok := got["Flockdeck.app/Contents/Info.plist"]
	if !ok {
		t.Fatal("the bundle has no Info.plist")
	}
	for _, want := range []string{
		"<key>CFBundleIdentifier</key>",
		"<string>" + darwinBundleID + "</string>",
		"<key>CFBundleExecutable</key>",
		"<string>flockdeck</string>",
		"<key>NSHighResolutionCapable</key>",
	} {
		if !strings.Contains(plist.body, want) {
			t.Errorf("Info.plist is missing %q:\n%s", want, plist.body)
		}
	}
	if strings.Contains(plist.body, "LSUIElement") {
		t.Error("Info.plist sets LSUIElement, which would hide Flockdeck's Dock icon -- it is a normal windowed app")
	}
	// The bundle already carries the binary; a loose copy at the archive's
	// root, as every other platform gets, would only double it for nothing
	// to unpack.
	if _, ok := got["flockdeck"]; ok {
		t.Error("the archive has a loose flockdeck at its root as well as the bundle's own copy")
	}

	// codesign checks the seal over Info.plist and the binary together,
	// which reading the archive's entries back individually above does not:
	// a bundle assembled with the right files but signed before one of them
	// was written, say, would still pass every check above and fail this
	// one.
	dest := t.TempDir()
	if out, err := exec.Command("tar", "-xzf", filepath.Join(out, name), "-C", dest).CombinedOutput(); err != nil {
		t.Fatalf("tar -xzf: %v\n%s", err, out)
	}
	bundle := filepath.Join(dest, "Flockdeck.app")
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", bundle).CombinedOutput(); err != nil {
		t.Errorf("codesign --verify %s: %v\n%s", bundle, err, out)
	}

	if entries, _ := os.ReadDir(out); len(entries) != 1 {
		t.Errorf("beside the archive: %v, want nothing else", entries)
	}
}

// darwinInfoPlist is pure string-building, so its output is checked as XML
// on every platform this runs on, not only a Mac: a stray unescaped
// character in the version or a mismatched tag would otherwise only be
// caught by a Mac's own plist reader, in CI or on a user's machine, long
// after this was written.
func TestDarwinInfoPlistIsWellFormedXML(t *testing.T) {
	for _, version := range []string{"v1.2.3", "v1.2.3-rc.1", "dev"} {
		plist := darwinInfoPlist(version)
		var doc struct {
			XMLName xml.Name `xml:"plist"`
			Dict    struct {
				Key    []string `xml:"key"`
				String []string `xml:"string"`
			} `xml:"dict"`
		}
		if err := xml.Unmarshal([]byte(plist), &doc); err != nil {
			t.Fatalf("darwinInfoPlist(%q) is not well-formed XML: %v\n%s", version, err, plist)
		}
		want := map[string]bool{
			"CFBundleIdentifier": false, "CFBundleName": false, "CFBundleExecutable": false,
			"CFBundleIconFile": false, "NSHighResolutionCapable": false,
		}
		for _, k := range doc.Dict.Key {
			want[k] = true
		}
		for k, found := range want {
			if !found {
				t.Errorf("darwinInfoPlist(%q) is missing <key>%s</key>", version, k)
			}
		}
	}
}

// A release is stamped with -X main.version, and the linker ignores an -X
// naming a variable that does not exist. Were main.version renamed, every
// release would ship stamped "dev", which the updater never replaces, and
// nobody would know until nobody updated. So the release's own build is run
// for this platform and the program is asked what it is.
func TestReleaseBuildStampsTheVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the program")
	}
	t.Chdir(filepath.Join("..", ".."))
	exe := filepath.Join(t.TempDir(), binary+ext(runtime.GOOS))
	if err := build("v9.9.9", runtime.GOOS, runtime.GOARCH, exe); err != nil {
		t.Fatalf("build: %v", err)
	}
	out, err := exec.Command(exe, "-version").Output()
	if err != nil {
		t.Fatalf("%s -version: %v", exe, err)
	}
	if !strings.Contains(string(out), "flockdeck v9.9.9") {
		t.Errorf("-version printed %q, want the version the release stamped", out)
	}
}

// Every module a release links has to be named in THIRD-PARTY-NOTICES.md,
// since each licence asks for its notice to travel with the binary. What is
// linked differs by platform — the PTY layer brings modules of its own on Linux
// and macOS — so every platform a release is built for is asked, not only the
// one the test runs on.
func TestNoticesNameEveryLinkedModule(t *testing.T) {
	if testing.Short() {
		t.Skip("asks the go command about every release platform")
	}
	t.Chdir(filepath.Join("..", ".."))
	notices, err := os.ReadFile("THIRD-PARTY-NOTICES.md")
	if err != nil {
		t.Fatal(err)
	}
	self, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	for _, p := range platforms {
		cmd := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", ".")
		cmd.Env = append(os.Environ(), "GOOS="+p.OS, "GOARCH="+p.Arch, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list for %s/%s: %v", p.OS, p.Arch, err)
		}
		seen := map[string]bool{}
		for _, m := range strings.Fields(string(out)) {
			if m == strings.TrimSpace(string(self)) || seen[m] {
				continue
			}
			seen[m] = true
			if !strings.Contains(string(notices), m) {
				t.Errorf("%s/%s links %s, which THIRD-PARTY-NOTICES.md does not name", p.OS, p.Arch, m)
			}
		}
	}
}

// Archives left by an earlier run of another version must not end up beside
// this run's, unlisted in its checksums, to be uploaded with it — but nothing
// else in the directory is this command's to remove.
func TestClearOldArchivesTakesOnlyItsOwn(t *testing.T) {
	out := t.TempDir()
	names := []string{"flockdeck_v0.9.0_linux_amd64.tar.gz", "flockdeck_v0.9.0_windows_amd64.zip", "checksums.txt",
		"checksums.txt.sig", "manifest.json", "manifest.json.sig", "latest.json", "notes.md"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(out, n), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := clearOldArchives(out); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		_, err := os.Stat(filepath.Join(out, n))
		if kept := err == nil; kept != (n == "notes.md") {
			t.Errorf("%s: kept = %v", n, kept)
		}
	}
	if err := clearOldArchives(t.TempDir()); err != nil {
		t.Errorf("an empty directory: %v", err)
	}
}

// selectPlatforms is what -platforms parses into the subset a CI runner's
// build job packages: an empty spec builds everything, as a developer's own
// machine still does, and a spec names the exact entries of platforms, in
// order, or is refused rather than silently building nothing asked for.
func TestSelectPlatformsParsesTheFlag(t *testing.T) {
	all, err := selectPlatforms("")
	if err != nil || len(all) != len(platforms) {
		t.Fatalf("empty spec: %v, %v, want every platform", all, err)
	}

	got, err := selectPlatforms("linux/amd64,linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ OS, Arch string }{{"linux", "amd64"}, {"linux", "arm64"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("linux/amd64,linux/arm64 = %v, want %v", got, want)
	}

	for _, bad := range []string{"solaris/amd64", "linux", "linux/amd64,not-a-pair", ""} {
		if bad == "" {
			continue // "" alone is the default, covered above
		}
		if _, err := selectPlatforms(bad); err == nil {
			t.Errorf("%q: no error, want one naming what is wrong", bad)
		}
	}
}

// A release built across several machines (one -platforms subset each)
// leaves each one's own partial checksums.txt behind; -sums (runSums)
// replaces it with one covering every archive actually sitting in -out,
// which is what sign then checks the release against.
func TestSumsChecksumsWhateverIsInOut(t *testing.T) {
	out := t.TempDir()
	files := map[string]string{
		"flockdeck_v1.0.0_linux_amd64.tar.gz": "linux build",
		"flockdeck_v1.0.0_windows_amd64.zip":  "windows build",
		"checksums.txt":                       "stale, from one machine's own partial build",
		"latest.json":                         `{"version":"v0.9.0"}`,
	}
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(out, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runSums(out); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(out, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, archive := range []string{"flockdeck_v1.0.0_linux_amd64.tar.gz", "flockdeck_v1.0.0_windows_amd64.zip"} {
		if !strings.Contains(string(sums), archive) {
			t.Errorf("checksums.txt = %q, want it to list %s", sums, archive)
		}
	}
	if strings.Contains(string(sums), "latest.json") {
		t.Errorf("checksums.txt = %q, should not list latest.json, which -sign writes rather than an archive", sums)
	}
}

func checkArchive(t *testing.T, kind string, got map[string]entry, programs ...string) {
	t.Helper()
	for _, p := range programs {
		bin, ok := got[p]
		switch {
		case !ok:
			t.Errorf("%s: no %s in %v", kind, p, got)
		case !bin.regular:
			t.Errorf("%s: %s is not a regular file, so the updater skips it", kind, p)
		case bin.mode&0o111 == 0:
			t.Errorf("%s: %s is mode %v, which will not run", kind, p, bin.mode)
		case bin.body != "the program":
			t.Errorf("%s: %s holds %q", kind, p, bin.body)
		}
	}
	for _, e := range extras {
		if _, ok := got[e]; !ok {
			t.Errorf("%s: %s is missing", kind, e)
		}
	}
}

func readTarGz(t *testing.T, path string) map[string]entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]entry{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = entry{h.Typeflag == tar.TypeReg, os.FileMode(h.Mode).Perm(), string(body)}
	}
}

func readZip(t *testing.T, path string) map[string]entry {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]entry{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = entry{f.Mode().IsRegular(), f.Mode().Perm(), string(body)}
	}
	return out
}
