package claudestate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadKeepsOnlyUsableFilesAndLooksPanesUp(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "10.json", `{"pid":10,"sessionId":"aaa","status":"busy","statusUpdatedAt":1792919636273,"startedAt":1792919630000}`)
	write(t, dir, "11.json", `{"pid":11,"sessionId":"bbb","status":"idle","statusUpdatedAt":1792919636999}`)
	write(t, dir, "12.json", `{"pid":12,"sessionId":"ccc","status":"thinking","statusUpdatedAt":1}`)
	write(t, dir, "13.json", `not json`)
	write(t, dir, "14.json", `{"pid":0,"sessionId":"ddd","status":"idle","statusUpdatedAt":1}`)
	write(t, dir, "10.abcdef.key", `secret`)

	got := Read(dir)
	if len(got) != 2 {
		t.Fatalf("read %d entries, want 2: %+v", len(got), got)
	}
	if e, ok := ReadPid(dir, 10); !ok || !e.Busy || e.SessionID != "aaa" || e.Started.IsZero() {
		t.Errorf("by pid: %+v %v", e, ok)
	}
	if _, ok := ReadPid(dir, 12); ok {
		t.Error("read a file whose status is neither busy nor idle")
	}
	if _, ok := ReadPid(dir, 99); ok {
		t.Error("read a file that is not there")
	}
	if e, ok := ByConversation(got, "bbb"); !ok || e.Busy || e.Pid != 11 {
		t.Errorf("by conversation: %+v %v", e, ok)
	}
	if _, ok := ByConversation(got, "zzz"); ok {
		t.Error("found a conversation that has no file")
	}
	if _, ok := ByConversation(got, ""); ok {
		t.Error("an empty conversation matched")
	}
	if got := Read(filepath.Join(dir, "nope")); got != nil {
		t.Errorf("a missing folder gave %v", got)
	}
}

// A file that is too large, or not a regular file, is not read, and the key
// files that sit beside the session files are never taken for them.
func TestReadRefusesWhatItShouldNotOpen(t *testing.T) {
	dir := t.TempDir()
	big := `{"pid":20,"sessionId":"x","status":"idle","statusUpdatedAt":1,"pad":"` + strings.Repeat("a", maxFile) + `"}`
	write(t, dir, "20.json", big)
	if _, ok := ReadPid(dir, 20); ok {
		t.Error("read a file over the size limit")
	}
	// A folder named like a session file is not a regular file.
	if err := os.Mkdir(filepath.Join(dir, "21.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadPid(dir, 21); ok {
		t.Error("read a folder")
	}
	// A symbolic link is not followed, where the platform allows making one.
	write(t, dir, "target.txt", `{"pid":22,"sessionId":"x","status":"idle","statusUpdatedAt":1}`)
	if err := os.Symlink(filepath.Join(dir, "target.txt"), filepath.Join(dir, "22.json")); err == nil {
		if _, ok := ReadPid(dir, 22); ok {
			t.Error("followed a symbolic link")
		}
	}
	// The pid in the name and in the file must agree.
	write(t, dir, "23.json", `{"pid":24,"sessionId":"x","status":"idle","statusUpdatedAt":1}`)
	if _, ok := ReadPid(dir, 23); ok {
		t.Error("read a file whose pid is not its name")
	}
}
