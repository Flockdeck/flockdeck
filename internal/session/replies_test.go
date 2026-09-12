package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestRecentRepliesReadsAnAPIPane covers a fan-out from an API pane. Its plan
// is in Flockdeck's own chat folder under the pane's id, and asking only Claude
// Code's store sent the fan-out to read the screen instead.
func TestRecentRepliesReadsAnAPIPane(t *testing.T) {
	base := t.TempDir()
	t.Setenv("APPDATA", base)
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(base, "claude"))

	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	chats := filepath.Join(dir, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	chat := `{"type":"user","text":"plan it"}` + "\n" + `{"type":"assistant","text":"- the first thing\n- the second"}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, id+".jsonl"), []byte(chat), 0o600); err != nil {
		t.Fatal(err)
	}

	got := RecentReplies(id, 1)
	if len(got) != 1 || !strings.Contains(got[0], "- the first thing") {
		t.Errorf("RecentReplies = %q, want the plan the API pane wrote", got)
	}
}
