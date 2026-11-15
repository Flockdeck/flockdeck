package claudeacp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	listIDa = "aaaaaaaa-5c2a-4a3e-9d7a-6f1f3f0b9c11"
	listIDb = "bbbbbbbb-5c2a-4a3e-9d7a-6f1f3f0b9c11"
	listIDc = "cccccccc-5c2a-4a3e-9d7a-6f1f3f0b9c11"
)

// storeNamed writes a stored session of cwd under id, with the given age.
func storeNamed(t *testing.T, projects, cwd, id string, age time.Duration, lines ...string) string {
	t.Helper()
	dir := filepath.Join(projects, nonWord.ReplaceAllString(cwd, "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListSessionsIsNewestFirstWithTheFirstPromptAsTitle(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	cwd := t.TempDir()
	storeNamed(t, projects, cwd, listIDa, 3*time.Hour, userLine("  first of a\nsecond line  "), assistantLine("m", textBlock("hi")))
	storeNamed(t, projects, cwd, listIDb, time.Hour, jl(map[string]any{"type": "summary"}), userLine("<command-name>/clear</command-name>"), userLine("fix the bug in b"))
	// Only prompts claude wrote itself, and one in a sidechain: nothing to list.
	storeNamed(t, projects, cwd, listIDc, 2*time.Hour, userLine("meta", "isMeta", "x"), userLine("side", "isSidechain", "x"))
	got := listSessions(projects, cwd)
	if len(got) != 2 || got[0].ID != listIDb || got[1].ID != listIDa {
		t.Fatalf("listed %+v", got)
	}
	if got[0].Title != "/clear" || got[1].Title != "first of a" {
		t.Errorf("titles %q, %q", got[0].Title, got[1].Title)
	}
	if got[1].Cwd != cwd {
		t.Errorf("cwd %q", got[1].Cwd)
	}
}

func TestListSessionsKeepsToTheFoldersOwnDirectoryAndRegularFiles(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	cwd, other := t.TempDir(), t.TempDir()
	storeNamed(t, projects, other, listIDa, time.Hour, userLine("belongs to another folder"))
	dir := filepath.Join(projects, nonWord.ReplaceAllString(cwd, "-"))
	if err := os.MkdirAll(filepath.Join(dir, listIDb+".jsonl"), 0o700); err != nil { // a directory
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.jsonl"), []byte(userLine("not a session id")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := listSessions(projects, cwd); len(got) != 0 {
		t.Fatalf("listed %+v", got)
	}
}

func TestListSessionsReportsTheFolderTheFileNames(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	cwd := t.TempDir()
	// claude spells the folder with dashes, so two folders can share a directory;
	// the line's own cwd is what tells them apart.
	storeNamed(t, projects, cwd, listIDa, time.Hour, jl(map[string]any{"type": "system", "cwd": "/somewhere/else"}), userLine("hello"))
	got := listSessions(projects, cwd)
	if len(got) != 1 || got[0].Cwd != "/somewhere/else" {
		t.Fatalf("listed %+v", got)
	}
}

func TestListSessionsCapsTheListAndTheTitle(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	cwd := t.TempDir()
	for i := 0; i < listMax+5; i++ {
		id := fmt.Sprintf("%08x-5c2a-4a3e-9d7a-6f1f3f0b9c11", i+1)
		storeNamed(t, projects, cwd, id, time.Duration(i)*time.Minute, userLine(strings.Repeat("é", 400)))
	}
	got := listSessions(projects, cwd)
	if len(got) > listMax {
		t.Fatalf("%d sessions", len(got))
	}
	for _, l := range got {
		if len(l.Title) > listTitleMax || !strings.HasPrefix(l.Title, "é") {
			t.Fatalf("title of %d bytes", len(l.Title))
		}
	}
}

func TestSessionListIsAdvertisedAndAnswered(t *testing.T) {
	h, projects := loadHarness(t)
	storeNamed(t, projects, h.cwd, listIDa, time.Hour, userLine("a prompt"))
	init := h.call("initialize", map[string]any{"protocolVersion": 1})
	if init.Error != nil || !strings.Contains(string(init.Result), `"list":{}`) {
		t.Fatalf("initialize: %s %v", init.Result, init.Error)
	}
	r := h.call("session/list", map[string]any{"cwd": h.cwd})
	var res struct {
		Sessions []struct {
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
			Title     string `json:"title"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(r.Result, &res); err != nil || len(res.Sessions) != 1 {
		t.Fatalf("answer %s (%v)", r.Result, err)
	}
	s := res.Sessions[0]
	if s.SessionID != listIDa || s.Title != "a prompt" || s.Cwd != h.cwd {
		t.Errorf("session %+v", s)
	}
	if _, err := time.Parse(time.RFC3339, s.UpdatedAt); err != nil {
		t.Errorf("updatedAt %q", s.UpdatedAt)
	}
	if bad := h.call("session/list", map[string]any{"cwd": "relative/path"}); bad.Error == nil {
		t.Error("a relative cwd was accepted")
	}
}
