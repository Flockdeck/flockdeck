package claudeacp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeInput(path, content string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"file_path": path, "content": content})
	return b
}

func contentTypes(d toolDesc) []string {
	var out []string
	for _, c := range d.content {
		out = append(out, c.(map[string]any)["type"].(string))
	}
	return out
}

// A Write to a file that is there and cannot be read must not be drawn as a new
// file: that says the write adds lines and removes none. Only a file that is not
// there is new.
func TestWriteOfAFileThatCannotBeReadHasNoDiff(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(existing, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", maxDiffRead+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path      string
		want      string
		oldIsNull bool
	}{
		"a file that is there": {existing, "diff", false},
		"a file that is not":   {filepath.Join(dir, "new.txt"), "diff", true},
		"a file too large":     {big, "content", false},
		"a directory":          {sub, "content", false},
	} {
		t.Run(name, func(t *testing.T) {
			d := describeTool("Write", writeInput(tc.path, "new\n"), dir, "")
			got := contentTypes(d)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("content = %v, want one %s", got, tc.want)
			}
			item := d.content[0].(map[string]any)
			if tc.want == "diff" {
				if (item["oldText"] == nil) != tc.oldIsNull {
					t.Errorf("oldText = %v, null wanted %v", item["oldText"], tc.oldIsNull)
				}
				return
			}
			inner := item["content"].(map[string]any)
			if inner["text"] != writeUnreadNote {
				t.Errorf("text = %v", inner["text"])
			}
		})
	}
}
