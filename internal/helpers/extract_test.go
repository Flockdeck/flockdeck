package helpers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testTop = "lens_0.4.0_linux_amd64"

func testRules() ExtractRules {
	return ExtractRules{Top: testTop, Binary: "lens", MaxFiles: 20, MaxTotal: 1 << 20, MaxFile: 1 << 20}
}

// extractInto unpacks into a folder that sits alone in a parent, and checks
// afterwards that nothing was written beside it. It returns the error.
func extractInto(t *testing.T, archive string, data []byte, rules ExtractRules) (root string, err error) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, "x")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), archive)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	err = Extract(file, root, rules)
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 || entries[0].Name() != "x" {
		t.Errorf("something was written outside the staging folder: %v", entries)
	}
	return root, err
}

func okMembers() []member {
	return []member{
		{name: testTop + "/", typ: tar.TypeDir},
		{name: testTop + "/lens", body: "binary", mode: 0o755},
		{name: testTop + "/README.md", body: "readme"},
	}
}

func TestExtractGoodArchives(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			var data []byte
			if format == "zip" {
				data = zipBytes(t, okMembers())
			} else {
				data = tarGz(t, okMembers())
			}
			root, err := extractInto(t, "a."+format, data, testRules())
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(root, testTop, "lens"))
			if err != nil || string(got) != "binary" {
				t.Fatalf("binary = %q, %v", got, err)
			}
		})
	}
}

func TestExtractWithoutDirectoryEntries(t *testing.T) {
	// Archives from many tools carry no entry for the folder itself.
	m := okMembers()[1:]
	if _, err := extractInto(t, "a.tar.gz", tarGz(t, m), testRules()); err != nil {
		t.Fatal(err)
	}
	if _, err := extractInto(t, "a.zip", zipBytes(t, m), testRules()); err != nil {
		t.Fatal(err)
	}
}

// badArchives are members that must each be refused, in both formats where the
// format can express them.
func TestExtractRefusesUnsafeArchives(t *testing.T) {
	bin := member{name: testTop + "/lens", body: "binary"}
	cases := []struct {
		name    string
		members []member
		tarOnly bool
		zipOnly bool
		reason  string
	}{
		{"dot dot", []member{bin, {name: testTop + "/../escape.txt", body: "x"}}, false, false, ""},
		{"leading dot dot", []member{bin, {name: "../escape.txt", body: "x"}}, false, false, ""},
		{"dot dot in the middle", []member{bin, {name: testTop + "/a/../../escape.txt", body: "x"}}, false, false, ""},
		{"absolute", []member{bin, {name: "/etc/escape.txt", body: "x"}}, false, false, "absolute"},
		{"drive letter", []member{bin, {name: "C:/escape.txt", body: "x"}}, false, false, "colon"},
		{"drive letter relative", []member{bin, {name: "C:escape.txt", body: "x"}}, false, false, "colon"},
		{"backslash separator", []member{bin, {name: testTop + `\..\escape.txt`, body: "x"}}, false, false, "backslash"},
		{"backslash alone", []member{bin, {name: testTop + `\x`, body: "x"}}, false, false, "backslash"},
		{"unc path", []member{bin, {name: `\\host\share\x`, body: "x"}}, false, false, ""},
		{"alternate data stream", []member{bin, {name: testTop + "/lens:hidden", body: "x"}}, false, false, "colon"},
		{"dot element", []member{bin, {name: testTop + "/./x", body: "x"}}, false, false, ""},
		{"empty element", []member{bin, {name: testTop + "//x", body: "x"}}, false, false, ""},
		{"trailing dot", []member{bin, {name: testTop + "/x.", body: "x"}}, false, false, "dot or a space"},
		{"trailing space", []member{bin, {name: testTop + "/x ", body: "x"}}, false, false, "dot or a space"},
		{"device name", []member{bin, {name: testTop + "/NUL", body: "x"}}, false, false, "reserved"},
		{"device name with extension", []member{bin, {name: testTop + "/con.txt", body: "x"}}, false, false, "reserved"},
		{"control character", []member{bin, {name: testTop + "/a\x01b", body: "x"}}, false, false, "control"},
		{"wrong top folder", []member{{name: "lens_0.4.1_linux_amd64/lens", body: "binary"}}, false, false, "not inside"},
		{"no top folder", []member{{name: "lens", body: "binary"}}, false, false, "not inside"},
		{"top folder prefix only", []member{{name: testTop + "x/lens", body: "binary"}}, false, false, "not inside"},
		{"top folder case differs", []member{{name: strings.ToUpper(testTop) + "/lens", body: "binary"}}, false, false, "not inside"},
		{"two top folders", []member{bin, {name: "other/x", body: "x"}}, false, false, "not inside"},
		{"missing binary", []member{{name: testTop + "/README.md", body: "x"}}, false, false, "does not hold"},
		{"binary in a subfolder", []member{{name: testTop + "/bin/lens", body: "x"}}, false, false, "does not hold"},
		{"duplicate member", []member{bin, bin}, false, false, "twice"},
		{"duplicate differing by case", []member{bin, {name: testTop + "/LENS", body: "other"}}, false, false, "twice"},
		{"file then folder of the same name", []member{bin, {name: testTop + "/a", body: "x"}, {name: testTop + "/a/b", body: "x"}}, false, false, "is a file"},
		{"symlink", []member{bin, {name: testTop + "/link", typ: tar.TypeSymlink, link: "/etc/passwd"}}, true, false, "symbolic link"},
		{"symlink to parent", []member{bin, {name: testTop + "/link", typ: tar.TypeSymlink, link: ".."}}, true, false, "symbolic link"},
		{"hardlink", []member{bin, {name: testTop + "/hard", typ: tar.TypeLink, link: testTop + "/lens"}}, true, false, "hard link"},
		{"hardlink outside", []member{bin, {name: testTop + "/hard", typ: tar.TypeLink, link: "../outside"}}, true, false, "hard link"},
		{"character device", []member{bin, {name: testTop + "/dev", typ: tar.TypeChar}}, true, false, "device"},
		{"block device", []member{bin, {name: testTop + "/dev", typ: tar.TypeBlock}}, true, false, "device"},
		{"named pipe", []member{bin, {name: testTop + "/pipe", typ: tar.TypeFifo}}, true, false, "named pipe"},
		{"zip symlink", []member{bin, {name: testTop + "/link", body: "/etc/passwd", zipMode: os.ModeSymlink | 0o777}}, false, true, "symbolic link"},
		{"zip device", []member{bin, {name: testTop + "/dev", zipMode: os.ModeDevice | 0o644}}, false, true, "device"},
		{"folder named like a file", []member{bin, {name: testTop + "/x", typ: tar.TypeDir}}, true, false, "disagree"},
		{"too deep", []member{bin, {name: testTop + "/" + strings.Repeat("d/", 20) + "f", body: "x"}}, false, false, "deep"},
		{"name too long", []member{bin, {name: testTop + "/" + strings.Repeat("n", 300), body: "x"}}, false, false, "longer"},
	}
	for _, c := range cases {
		formats := []string{"tar.gz", "zip"}
		if c.tarOnly {
			formats = formats[:1]
		}
		if c.zipOnly {
			formats = formats[1:]
		}
		for _, format := range formats {
			t.Run(c.name+"/"+format, func(t *testing.T) {
				var data []byte
				if format == "zip" {
					data = zipBytes(t, c.members)
				} else {
					data = tarGz(t, c.members)
				}
				_, err := extractInto(t, "a."+format, data, testRules())
				if err == nil {
					t.Fatal("the archive was accepted")
				}
				var ae *ArchiveError
				if !errors.As(err, &ae) {
					t.Fatalf("err = %v, want an ArchiveError", err)
				}
				if c.reason != "" && !strings.Contains(err.Error(), c.reason) {
					t.Fatalf("err = %v, want it to mention %q", err, c.reason)
				}
			})
		}
	}
}

func TestExtractRefusesSizes(t *testing.T) {
	rules := testRules()
	rules.MaxFile = 100
	rules.MaxTotal = 150
	rules.MaxFiles = 4
	big := func(n int) string { return strings.Repeat("a", n) }
	cases := []struct {
		name    string
		members []member
	}{
		{"one file over the file cap", []member{{name: testTop + "/lens", body: big(101)}}},
		{"files over the total cap", []member{{name: testTop + "/lens", body: big(90)}, {name: testTop + "/a", body: big(90)}}},
		{"too many members", []member{{name: testTop + "/lens", body: "x"}, {name: testTop + "/a", body: "x"}, {name: testTop + "/b", body: "x"},
			{name: testTop + "/c", body: "x"}, {name: testTop + "/d", body: "x"}}},
	}
	for _, c := range cases {
		for _, format := range []string{"tar.gz", "zip"} {
			t.Run(c.name+"/"+format, func(t *testing.T) {
				var data []byte
				if format == "zip" {
					data = zipBytes(t, c.members)
				} else {
					data = tarGz(t, c.members)
				}
				if _, err := extractInto(t, "a."+format, data, rules); err == nil {
					t.Fatal("the archive was accepted")
				}
			})
		}
	}
}

func TestExtractStopsAtTheCapWhateverTheHeaderSays(t *testing.T) {
	// The copy is capped by the limit as it is read, not by the size a header
	// claims: 5000 bytes of one letter pack to almost nothing.
	rules := testRules()
	rules.MaxFile = 1000
	rules.MaxTotal = 1000
	data := zipBytes(t, []member{{name: testTop + "/lens", body: strings.Repeat("z", 5000)}})
	if _, err := extractInto(t, "a.zip", data, rules); err == nil {
		t.Fatal("an archive that unpacks past the cap was accepted")
	}
	data = tarGz(t, []member{{name: testTop + "/lens", body: strings.Repeat("z", 5000)}})
	if _, err := extractInto(t, "a.tar.gz", data, rules); err == nil {
		t.Fatal("an archive that unpacks past the cap was accepted")
	}
}

func TestExtractNotAnArchive(t *testing.T) {
	for _, name := range []string{"a.tar.gz", "a.zip"} {
		if _, err := extractInto(t, name, []byte("this is not an archive"), testRules()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := extractInto(t, "a.tar.gz", nil, testRules()); err == nil {
		t.Error("an empty file was accepted")
	}
}

func TestExtractFileModes(t *testing.T) {
	root, err := extractInto(t, "a.tar.gz", tarGz(t, okMembers()), testRules())
	if err != nil {
		t.Fatal(err)
	}
	// Extract itself writes 0644 and 0755; setModes finalises them.
	if err := setModes(filepath.Join(root, testTop), "lens"); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]fs.FileMode{
			filepath.Join(root, testTop):              0o755,
			filepath.Join(root, testTop, "lens"):      0o755,
			filepath.Join(root, testTop, "README.md"): 0o644,
		} {
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != want {
				t.Errorf("%s is %v, want %v", path, fi.Mode().Perm(), want)
			}
		}
	}
}

// A zip whose header says a file is small while its data is not. The copy is
// capped by the limit as it is read, not by what the header claims.
func TestExtractZipThatLiesAboutSize(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	body := []byte(strings.Repeat("z", 5000))
	var raw bytes.Buffer
	fw, _ := flate.NewWriter(&raw, flate.BestCompression)
	_, _ = fw.Write(body)
	_ = fw.Close()
	hdr := &zip.FileHeader{Name: testTop + "/lens", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(body)}
	hdr.UncompressedSize64 = 10
	hdr.CompressedSize64 = uint64(raw.Len())
	hdr.SetMode(0o755)
	w, err := zw.CreateRaw(hdr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(raw.Bytes())
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	rules := testRules()
	rules.MaxFile, rules.MaxTotal = 1000, 1000
	root, err := extractInto(t, "a.zip", buf.Bytes(), rules)
	if err == nil {
		t.Fatal("a lying header got 5000 bytes past a 1000 byte cap")
	}
	if fi, statErr := os.Stat(filepath.Join(root, testTop, "lens")); statErr == nil && fi.Size() > 1001 {
		t.Fatalf("%d bytes were written", fi.Size())
	}
}

// writeFile's own cap, for a reader that gives more than its header said.
func TestWriteFileCapsWhatIsReadWhateverIsDeclared(t *testing.T) {
	rules := testRules()
	rules.MaxFile, rules.MaxTotal = 1000, 1500
	x := newExtractor(rules, t.TempDir())
	err := x.writeFile([]string{testTop, "a"}, 10, strings.NewReader(strings.Repeat("z", 5000)))
	var ae *ArchiveError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an ArchiveError", err)
	}
	// The total cap holds across files too: 900 + 900 is over 1500.
	x = newExtractor(rules, t.TempDir())
	if err := x.writeFile([]string{testTop, "a"}, 900, strings.NewReader(strings.Repeat("z", 900))); err != nil {
		t.Fatal(err)
	}
	if err := x.writeFile([]string{testTop, "b"}, 10, strings.NewReader(strings.Repeat("z", 900))); err == nil {
		t.Fatal("the total cap was not applied to what was read")
	}
}
