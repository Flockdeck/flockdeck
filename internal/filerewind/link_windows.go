package filerewind

import (
	"path/filepath"
	"strings"
	"syscall"
)

// Attributes of a file that is not fully on this disk: a OneDrive or other
// cloud placeholder. Reading one makes the provider download it, so it is
// treated as a link and left alone.
const (
	attrOffline          = 0x1000
	attrRecallOnOpen     = 0x40000
	attrRecallOnDataAcc  = 0x400000
	placeholderAttrsMask = attrOffline | attrRecallOnOpen | attrRecallOnDataAcc
)

// linkAttrs says the attributes are those of a symbolic link, a junction or any
// other reparse point (cloud placeholders included), or of an offline file.
func linkAttrs(attr uint32) bool {
	return attr&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 || attr&placeholderAttrsMask != 0
}

// isLink says path is a symbolic link, a junction, another reparse point or a
// cloud placeholder. Go's Lstat reports a junction as a plain folder, so the
// attribute is read. When the attribute cannot be read, the answer is yes: a
// path that cannot be examined is not one to write through.
func isLink(path string) bool {
	p, err := syscall.UTF16PtrFromString(longPath(path))
	if err != nil {
		return true
	}
	attr, err := syscall.GetFileAttributes(p)
	if err != nil {
		return true
	}
	return linkAttrs(attr)
}

// longPath spells an absolute path so that the Windows calls take it past 260
// characters.
func longPath(path string) string {
	if len(path) < 248 || strings.HasPrefix(path, `\\?\`) || !filepath.IsAbs(path) {
		return path
	}
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
