package claudestate

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadKeepsOnlyUsableFilesAndFindMatchesPidThenConversation(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "10.json", `{"pid":10,"sessionId":"aaa","status":"busy","statusUpdatedAt":1792919636273}`)
	write(t, dir, "11.json", `{"pid":11,"sessionId":"bbb","status":"idle","statusUpdatedAt":1792919636999}`)
	write(t, dir, "12.json", `{"pid":12,"sessionId":"ccc","status":"thinking","statusUpdatedAt":1}`)
	write(t, dir, "13.json", `not json`)
	write(t, dir, "14.json", `{"pid":0,"sessionId":"ddd","status":"idle","statusUpdatedAt":1}`)
	write(t, dir, "10.abcdef.key", `secret`)

	got := Read(dir)
	if len(got) != 2 {
		t.Fatalf("read %d entries, want 2: %+v", len(got), got)
	}
	if e, ok := Find(got, 10, "other"); !ok || !e.Busy {
		t.Errorf("by pid: %+v %v", e, ok)
	}
	if e, ok := Find(got, 99, "bbb"); !ok || e.Busy || e.Pid != 11 {
		t.Errorf("by conversation: %+v %v", e, ok)
	}
	if _, ok := Find(got, 99, "zzz"); ok {
		t.Error("found a pane that has no file")
	}
	if got := Read(filepath.Join(dir, "nope")); got != nil {
		t.Errorf("a missing folder gave %v", got)
	}
}
