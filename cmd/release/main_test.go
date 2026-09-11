package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
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
