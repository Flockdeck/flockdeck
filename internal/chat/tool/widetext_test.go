package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// encodeUTF16 is s as Windows PowerShell's > writes it: UTF-16 behind a
// byte-order mark.
func encodeUTF16(s string, bigEndian bool) []byte {
	out := []byte{0xff, 0xfe}
	if bigEndian {
		out = []byte{0xfe, 0xff}
	}
	for _, u := range utf16.Encode([]rune(s)) {
		if bigEndian {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out
}

// A log saved from Windows PowerShell is UTF-16, and is read and searched as
// the text it is rather than refused as binary for its NUL bytes.
func TestUTF16TextIsReadAndSearchedAsText(t *testing.T) {
	root := newRoot(t)
	text := "hello\r\nwörld 😀\r\n"
	for name, big := range map[string]bool{"le.log": false, "be.log": true} {
		if err := os.WriteFile(filepath.Join(root.Dir(), name), encodeUTF16(text, big), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := call(t, &readFile{root: root}, map[string]any{"path": name})
		if err != nil || !strings.Contains(got, "1\thello\n2\twörld 😀\n") {
			t.Errorf("read_file %s = %q, %v", name, got, err)
		}
	}

	got, err := call(t, &grepTool{root: root}, map[string]any{"pattern": "wörld", "path": "le.log"})
	if err != nil || !strings.Contains(got, "le.log:2: wörld") {
		t.Errorf("grep = %q, %v", got, err)
	}

	// An edit would have to be written back as UTF-16, and is refused saying so.
	_, err = call(t, &editFile{root: root}, map[string]any{"path": "le.log", "old_string": "hello", "new_string": "bye"})
	if err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Errorf("edit_file on UTF-16: %v", err)
	}
}
