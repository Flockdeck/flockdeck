package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/filerewind"
	"github.com/jmwri/flockdeck/internal/structured"
)

// A restore refused after the conversation went back says which files and why.
func TestRestoreViewListsTheBlockedFiles(t *testing.T) {
	items := []filerewind.BlockedItem{
		{Path: "assets/logo.png", Reason: filerewind.ReasonFilter},
		{Path: "gen.txt", Reason: filerewind.ReasonIgnored},
	}
	v := restoreView(structured.FilesOutcome{
		Result: filerewind.Result{Kept: "refs/flockdeck/rewind-undo/kept/r/1"},
		Err:    fmt.Errorf("restore: %w", &filerewind.BlockedError{Items: items}),
	})
	if len(v.Blocked) != 2 || v.Blocked[0].Path != "assets/logo.png" || v.Blocked[1].Reason != filerewind.ReasonIgnored {
		t.Fatalf("blocked = %+v", v.Blocked)
	}
	if !strings.Contains(v.Error, "could not be put back safely") || v.Kept == "" {
		t.Errorf("error = %q, kept = %q", v.Error, v.Kept)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"blocked":[{"path":"assets/logo.png"`) {
		t.Errorf("json = %s", b)
	}
	if !strings.Contains(filerewind.ReasonFilter, "Git LFS") {
		t.Error("the reason for a filtered file does not say LFS")
	}
	plain := restoreView(structured.FilesOutcome{Result: filerewind.Result{Written: 2}})
	if plain.Blocked != nil || plain.Error != "" {
		t.Errorf("a clean restore has %+v", plain)
	}
	if text, ok := filesRefusal(filerewind.ErrSlowNow); !ok || text != structured.FilesWhySlowNow || strings.Contains(text, "were not saved") {
		t.Errorf("slow now = %q", text)
	}
}
