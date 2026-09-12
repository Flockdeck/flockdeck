package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	if err := writeTarGz(tgz, built, "flockdeck"); err != nil {
		t.Fatal(err)
	}
	checkArchive(t, "tar.gz", readTarGz(t, tgz), "flockdeck")

	zp := filepath.Join(dir, "flockdeck.zip")
	if err := writeZip(zp, built, "flockdeck.exe"); err != nil {
		t.Fatal(err)
	}
	checkArchive(t, "zip", readZip(t, zp), "flockdeck.exe")
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
	names := []string{"flockdeck_v0.9.0_linux_amd64.tar.gz", "flockdeck_v0.9.0_windows_amd64.zip", "checksums.txt", "notes.md"}
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

func checkArchive(t *testing.T, kind string, got map[string]entry, binary string) {
	t.Helper()
	bin, ok := got[binary]
	switch {
	case !ok:
		t.Errorf("%s: no %s in %v", kind, binary, got)
	case !bin.regular:
		t.Errorf("%s: %s is not a regular file, so the updater skips it", kind, binary)
	case bin.mode&0o111 == 0:
		t.Errorf("%s: %s is mode %v, which will not run", kind, binary, bin.mode)
	case bin.body != "the program":
		t.Errorf("%s: %s holds %q", kind, binary, bin.body)
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
