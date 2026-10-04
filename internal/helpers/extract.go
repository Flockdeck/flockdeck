package helpers

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ExtractRules say what an archive may hold.
type ExtractRules struct {
	// Top is the one folder every member must be inside, exactly as named.
	Top string
	// Binary is the file, directly inside Top, that must be present.
	Binary string
	// MaxFiles caps the members, MaxTotal the bytes they unpack to and MaxFile
	// the bytes of any one.
	MaxFiles int
	MaxTotal int64
	MaxFile  int64
}

// ArchiveError is what extraction fails with when the archive is not
// acceptable. Nothing is run after one, and the staging folder is removed by
// the caller.
type ArchiveError struct{ Reason string }

func (e *ArchiveError) Error() string { return "unsafe archive: " + e.Reason }

func unsafef(format string, args ...any) error {
	return &ArchiveError{fmt.Sprintf(format, args...)}
}

const (
	maxNameLen  = 255
	maxPathLen  = 1024
	maxPathDeep = 16
)

// reservedNames are the Windows device names. A file called one of these, with
// or without an extension, is not a file there, and what it does instead is
// not for an archive to decide.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// cleanMember checks a member's name and returns its path elements, the first
// of which is the top folder. The rules are the strictest of the platforms
// Flockdeck runs on, applied everywhere, so an archive that is fine on Linux
// and escapes on Windows is refused on both.
func cleanMember(name string, top string) (elems []string, isDirName bool, err error) {
	if name == "" {
		return nil, false, unsafef("a member has no name")
	}
	if len(name) > maxPathLen {
		return nil, false, unsafef("a member's path is %d bytes long", len(name))
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return nil, false, unsafef("%q contains a control character", name)
		}
	}
	if strings.Contains(name, `\`) {
		return nil, false, unsafef("%q contains a backslash", name)
	}
	if strings.Contains(name, ":") {
		// Drive letters ("C:x"), alternate data streams ("a:b") and device paths.
		return nil, false, unsafef("%q contains a colon", name)
	}
	if strings.HasPrefix(name, "/") {
		return nil, false, unsafef("%q is an absolute path", name)
	}
	isDirName = strings.HasSuffix(name, "/")
	elems = strings.Split(strings.TrimSuffix(name, "/"), "/")
	if len(elems) > maxPathDeep {
		return nil, false, unsafef("%q is nested %d folders deep", name, len(elems))
	}
	for _, el := range elems {
		switch {
		case el == "" || el == "." || el == "..":
			return nil, false, unsafef("%q has an empty, \".\" or \"..\" element", name)
		case len(el) > maxNameLen:
			return nil, false, unsafef("%q has an element longer than %d bytes", name, maxNameLen)
		case strings.HasSuffix(el, ".") || strings.HasSuffix(el, " "):
			return nil, false, unsafef("%q has an element ending in a dot or a space", name)
		}
		stem := el
		if i := strings.IndexByte(stem, '.'); i >= 0 {
			stem = stem[:i]
		}
		if reservedNames[strings.ToUpper(stem)] {
			return nil, false, unsafef("%q uses the reserved name %s", name, stem)
		}
	}
	if elems[0] != top {
		return nil, false, unsafef("%q is not inside the folder %q", name, top)
	}
	return elems, isDirName, nil
}

// extractor is the state shared by the tar and zip readers: what has been seen
// and how much has been written.
type extractor struct {
	rules ExtractRules
	root  string // the folder members are written into
	// seen maps a lowercased path to the name as the archive wrote it, and kind
	// to what that path is, "dir" or "file".
	seen  map[string]string
	kind  map[string]string
	files int
	total int64
}

func newExtractor(rules ExtractRules, root string) *extractor {
	return &extractor{rules: rules, root: root, seen: map[string]string{}, kind: map[string]string{}}
}

// dest returns the path for a member's elements below root.
func (x *extractor) dest(elems []string) string {
	return filepath.Join(append([]string{x.root}, elems...)...)
}

// claim records a member's name, refusing a second one that is the same name
// or differs only by case (two names for one file on a case-insensitive disk),
// and a path that is both a file and the folder of another member.
func (x *extractor) claim(elems []string, dir bool) error {
	joined := strings.Join(elems, "/")
	key := strings.ToLower(joined)
	if prev, dup := x.seen[key]; dup {
		return unsafef("%q appears twice", prev)
	}
	x.seen[key] = joined
	for i := 1; i < len(elems); i++ {
		parent := strings.ToLower(strings.Join(elems[:i], "/"))
		if k, ok := x.kind[parent]; ok && k != "dir" {
			return unsafef("%q is inside %q, which is a file", joined, strings.Join(elems[:i], "/"))
		}
	}
	if dir {
		x.kind[key] = "dir"
	} else {
		x.kind[key] = "file"
	}
	return nil
}

func (x *extractor) count() error {
	x.files++
	if x.rules.MaxFiles > 0 && x.files > x.rules.MaxFiles {
		return unsafef("more than %d members", x.rules.MaxFiles)
	}
	return nil
}

// writeFile writes one member's content, stopping at the caps whatever the
// archive's own headers claim. The file is created new (O_EXCL), so a member
// is never written through something that already exists.
func (x *extractor) writeFile(elems []string, declared int64, r io.Reader) error {
	name := strings.Join(elems, "/")
	if declared < 0 {
		return unsafef("%q has a negative size", name)
	}
	if x.rules.MaxFile > 0 && declared > x.rules.MaxFile {
		return unsafef("%q is %d bytes, over the %d byte limit for one file", name, declared, x.rules.MaxFile)
	}
	if x.rules.MaxTotal > 0 && x.total+declared > x.rules.MaxTotal {
		return unsafef("the archive unpacks to more than %d bytes", x.rules.MaxTotal)
	}
	path := x.dest(elems)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	limit := int64(1<<62 - 1)
	if x.rules.MaxTotal > 0 {
		limit = x.rules.MaxTotal - x.total
	}
	if x.rules.MaxFile > 0 && x.rules.MaxFile < limit {
		limit = x.rules.MaxFile
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > limit {
		return unsafef("%q unpacks to more than the limits allow", name)
	}
	x.total += n
	return nil
}

func (x *extractor) mkdir(elems []string) error {
	return os.MkdirAll(x.dest(elems), 0o755)
}

// finish checks the archive held what it must.
func (x *extractor) finish() error {
	bin := filepath.Join(x.root, x.rules.Top, x.rules.Binary)
	fi, err := os.Lstat(bin)
	if err != nil || !fi.Mode().IsRegular() {
		return unsafef("the archive does not hold %s/%s", x.rules.Top, x.rules.Binary)
	}
	return nil
}

// Extract unpacks archive, a .tar.gz or a .zip by its name, into root, which
// must be a new empty folder made for the purpose. The archive is accepted
// only if the error is nil; on an error the caller removes root. Nothing here
// runs anything it writes.
func Extract(archive, root string, rules ExtractRules) error {
	x := newExtractor(rules, root)
	if strings.HasSuffix(archive, ".zip") {
		return x.zip(archive)
	}
	return x.tar(archive)
}

func (x *extractor) tar(archive string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return unsafef("not a gzip file: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return unsafef("unreadable tar: %v", err)
		}
		if err := x.count(); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeReg, tar.TypeDir:
		default:
			return unsafef("%q is a %s, not a plain file or folder", h.Name, tarKind(h.Typeflag))
		}
		isDir := h.Typeflag == tar.TypeDir
		elems, dirName, err := cleanMember(h.Name, x.rules.Top)
		if err != nil {
			return err
		}
		if dirName != isDir {
			// The name and the type disagree, and which one is believed decides
			// what gets created.
			return unsafef("%q: its name and its type disagree about being a folder", h.Name)
		}
		if err := x.claim(elems, isDir); err != nil {
			return err
		}
		if isDir {
			if err := x.mkdir(elems); err != nil {
				return err
			}
			continue
		}
		if len(elems) == 1 {
			return unsafef("%q is a file where the folder %q must be", h.Name, x.rules.Top)
		}
		if err := x.writeFile(elems, h.Size, tr); err != nil {
			return err
		}
	}
	return x.finish()
}

func tarKind(t byte) string {
	switch t {
	case tar.TypeSymlink:
		return "symbolic link"
	case tar.TypeLink:
		return "hard link"
	case tar.TypeChar, tar.TypeBlock:
		return "device"
	case tar.TypeFifo:
		return "named pipe"
	case tar.TypeXGlobalHeader:
		return "global header"
	}
	return fmt.Sprintf("member of type %q", string(rune(t)))
}

func (x *extractor) zip(archive string) error {
	zr, err := zip.OpenReader(archive)
	// ErrInsecurePath comes with a usable reader. The checks in cleanMember are
	// what refuse such a name, and they say why.
	if err != nil && (zr == nil || !errors.Is(err, zip.ErrInsecurePath)) {
		return unsafef("not a zip file: %v", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if err := x.count(); err != nil {
			return err
		}
		mode := zf.Mode()
		isDir := mode.IsDir()
		if !isDir && !mode.IsRegular() {
			return unsafef("%q is a %s, not a plain file or folder", zf.Name, zipKind(mode))
		}
		elems, dirName, err := cleanMember(zf.Name, x.rules.Top)
		if err != nil {
			return err
		}
		if dirName != isDir {
			return unsafef("%q: its name and its type disagree about being a folder", zf.Name)
		}
		if err := x.claim(elems, isDir); err != nil {
			return err
		}
		if isDir {
			if err := x.mkdir(elems); err != nil {
				return err
			}
			continue
		}
		if len(elems) == 1 {
			return unsafef("%q is a file where the folder %q must be", zf.Name, x.rules.Top)
		}
		if zf.UncompressedSize64 > 1<<62 {
			return unsafef("%q declares an absurd size", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return unsafef("%q is unreadable: %v", zf.Name, err)
		}
		err = x.writeFile(elems, int64(zf.UncompressedSize64), rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return x.finish()
}

func zipKind(m fs.FileMode) string {
	switch {
	case m&fs.ModeSymlink != 0:
		return "symbolic link"
	case m&fs.ModeDevice != 0, m&fs.ModeCharDevice != 0:
		return "device"
	case m&fs.ModeNamedPipe != 0:
		return "named pipe"
	case m&fs.ModeSocket != 0:
		return "socket"
	}
	return "special file"
}
