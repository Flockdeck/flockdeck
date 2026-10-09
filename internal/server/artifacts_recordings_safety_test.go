package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/artifacts"
)

func recNames(items []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, it := range items {
		if n, ok := it["name"].(string); ok {
			out[n] = true
		}
	}
	return out
}

// A file in another format version is not offered: it could not be opened, and
// listing it would only tell a device that it exists.
func TestRecordingsListLeavesOutOtherFormatVersions(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	for _, v := range []string{"1", "3", "99"} {
		writeRec(t, "shop-0a1b2c3d", "20261002T101530Z-eeee000"+v[:1]+".jsonl",
			strings.Replace(recStarted(), `"v":2`, `"v":`+v, 1))
	}
	// Not a start line at all.
	writeRec(t, "shop-0a1b2c3d", "20261003T101530Z-eeee0010.jsonl", recLine(1, "assistant_message", map[string]any{"text": "x"}))
	conn, sess := e.open(t)
	got := recNames(itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})))
	if len(got) != 1 || !got["20261001T101530Z-0123abcd.jsonl"] {
		t.Errorf("listed %v, want only the version 2 recording", got)
	}
}

// Names the viewer's own checks refuse are not offered, whether or not the
// system they were made on would have allowed them: a device reading the list
// learns nothing about them.
func TestRecordingsListLeavesOutNamesTheViewerRefuses(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	var bad []string
	for _, n := range []string{"con.jsonl", "NUL.jsonl", "com1.jsonl", "a:b.jsonl", "ctl.jsonl", "star*.jsonl"} {
		p := filepath.Join(t.TempDir(), n)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			continue // this system cannot hold the name at all
		}
		_ = os.Remove(p)
		writeRec(t, "shop-0a1b2c3d", n, recStarted())
		bad = append(bad, n)
	}
	if len(bad) == 0 {
		t.Skip("this system holds none of the refused names")
	}
	conn, sess := e.open(t)
	got := recNames(itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})))
	for _, n := range bad {
		if got[n] {
			t.Errorf("%q was listed", n)
		}
	}
	if !got["20261001T101530Z-0123abcd.jsonl"] {
		t.Errorf("the ordinary recording is missing from %v", got)
	}
}

func TestRecordingsListLeavesOutLinks(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	good := writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	outside := t.TempDir()
	target := filepath.Join(outside, "outside.jsonl")
	if err := os.WriteFile(target, []byte(recStarted()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(filepath.Dir(good), "20261002T101530Z-eeee0001.jsonl")); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	// A whole project folder that is a link to somewhere else.
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(filepath.Dir(good)), "linked-0a1b2c3d")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "20261003T101530Z-eeee0002.jsonl"), []byte(recStarted()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conn, sess := e.open(t)
	got := recNames(itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})))
	if len(got) != 1 || !got["20261001T101530Z-0123abcd.jsonl"] {
		t.Errorf("listed %v, want only the file that is really in the folder", got)
	}
}

// A listing that began after its time was spent offers nothing and does not
// walk the folder.
func TestScanRecordingsStopsWhenItsContextIsDone(t *testing.T) {
	newArtifactEnv(t)
	for i := 0; i < 5; i++ {
		writeRec(t, "shop-0a1b2c3d", "2026100"+string(rune('1'+i))+"T101530Z-0123abcd.jsonl", recStarted())
	}
	if got := scanRecordings(context.Background()); len(got) != 5 {
		t.Fatalf("setup: %d listed, want 5", len(got))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := scanRecordings(ctx); len(got) != 0 {
		t.Errorf("a done context still listed %d", len(got))
	}
}

// The time a request has is the time its recording work has: once it is spent,
// no page is read and nothing is recorded as shown.
func TestOpenRecordingHonoursTheRequestsDeadline(t *testing.T) {
	e := newArtifactEnv(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted(),
		recLine(2, "assistant_message", map[string]any{"text": "one"}))
	items := scanRecordings(context.Background())
	if len(items) != 1 {
		t.Fatalf("setup: %d listed", len(items))
	}
	entry := artifacts.Entry{Kind: recordingsKind, Path: items[0].path}
	req := artifactRequest{Op: "open", Kind: recordingsKind, ID: "x"}

	if r := e.srv.openRecording(context.Background(), "d1", "phone", entry, req); r["op"] != "data" {
		t.Fatalf("setup: an open with time left = %v", r)
	}
	before := auditLines(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	r := e.srv.openRecording(ctx, "d1", "phone", entry, req)
	if r["code"] != artifactErrUnavailable {
		t.Errorf("an open past its deadline = %v", r)
	}
	if after := auditLines(t); after != before {
		t.Error("a view that was never shown was recorded as shown")
	}
}

// End to end: a request held until its deadline, then released, is answered as
// timed out and the work it then runs is not done or recorded.
func TestRecordingsOpenPastTheRequestDeadlineShowsNothing(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted(),
		recLine(2, "assistant_message", map[string]any{"text": "one"}))
	e.srv.artifacts.setTimings(func(tm *artifactTimings) {
		tm.request = 150 * time.Millisecond
		tm.hook = func(ctx context.Context, op string) {
			if op == "open" {
				<-ctx.Done()
			}
		}
	})
	conn, sess := e.open(t)
	id := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))[0]["id"]
	before := auditLines(t)

	r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id})
	if r["code"] != artifactErrTimeout {
		t.Errorf("reply = %v, want a timeout", r)
	}
	if _, has := r["entries"]; has {
		t.Error("entries were sent after the deadline")
	}
	if after := auditLines(t); after != before {
		t.Error("a view that timed out was recorded as shown")
	}
}

// The viewer is a socket of its own and adds no control command that a device
// limited to viewing could send: whatever sets it up is for the desk alone.
func TestArtifactAndRecordingCommandsAreForFullAccessOnly(t *testing.T) {
	found := 0
	for name, acc := range commandAccess {
		l := strings.ToLower(name)
		if strings.Contains(l, "artifact") || strings.Contains(l, "recording") || strings.Contains(l, "recordview") {
			found++
			if acc != accessFull {
				t.Errorf("commandAccess[%q] = %v, want accessFull", name, acc)
			}
		}
	}
	if found == 0 {
		t.Fatal("no artifact command found in the table")
	}
}
