package creds

import (
	"os"
	"testing"
)

// A keys.json saved by Notepad or by PowerShell starts with a byte-order mark,
// and must read as the keys in it rather than as a broken file that refuses
// every later `keys set`.
func TestAStoreWithAByteOrderMarkStillReads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("\xef\xbb\xbf{\"anthropic\": \"sk-by-hand\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := stored("anthropic"); got != "sk-by-hand" {
		t.Errorf("stored = %q, want the key written by hand", got)
	}
	if err := Set("openai", "sk-new"); err != nil {
		t.Fatalf("Set refused a store with a byte-order mark: %v", err)
	}
	if got := stored("anthropic"); got != "sk-by-hand" {
		t.Errorf("setting another key lost the one written by hand: %q", got)
	}
}
