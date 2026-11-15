package filerewind

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestIsLinkFailsClosedAndTakesLongPaths(t *testing.T) {
	dir := t.TempDir()
	if !isLink(filepath.Join(dir, "does-not-exist.txt")) {
		t.Error("a path whose attributes cannot be read is not treated as a link")
	}
	// A file under a path over 260 characters is read through the long form.
	deep := dir
	for len(deep) < 300 {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	file := filepath.Join(deep, "f.txt")
	if err := os.MkdirAll(longPath(deep), 0o755); err != nil {
		t.Skipf("cannot make a long path here: %v", err)
	}
	if err := os.WriteFile(longPath(file), []byte("x"), 0o644); err != nil {
		t.Skipf("cannot make a long path here: %v", err)
	}
	if isLink(file) {
		t.Error("a plain file at a long path is reported as a link")
	}
	if isLink(deep) {
		t.Error("a plain folder at a long path is reported as a link")
	}
}

func TestPlaceholderAttributesAreLinks(t *testing.T) {
	for name, attr := range map[string]uint32{
		"reparse point":        syscall.FILE_ATTRIBUTE_REPARSE_POINT,
		"recall on data":       attrRecallOnDataAcc,
		"recall on open":       attrRecallOnOpen,
		"offline":              attrOffline,
		"placeholder + normal": attrRecallOnDataAcc | syscall.FILE_ATTRIBUTE_ARCHIVE,
	} {
		if !linkAttrs(attr) {
			t.Errorf("%s is not treated as a link", name)
		}
	}
	if linkAttrs(syscall.FILE_ATTRIBUTE_ARCHIVE) || linkAttrs(syscall.FILE_ATTRIBUTE_DIRECTORY) {
		t.Error("a plain file or folder is treated as a link")
	}
}
