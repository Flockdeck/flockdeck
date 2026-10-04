package record

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// newTestManager writes under a temporary directory of its own, never the
// real state directory.
func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m := NewManager(func() (string, error) { return dir, nil })
	// Registered after TempDir's own cleanup, so it runs first: an open file
	// cannot be removed on Windows.
	t.Cleanup(m.Close)
	return m, dir
}

var testMeta = Meta{Project: "shop", ProjectRoot: "/work/shop", Agent: "claude", Conversation: "conv-1"}

// feed writes the events of a made-up conversation, a second apart.
type feed struct {
	t    *testing.T
	m    *Manager
	meta Meta
	at   time.Time
}

func newFeed(t *testing.T, m *Manager) *feed {
	return &feed{t: t, m: m, meta: testMeta, at: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
}

// send writes one event and returns what Write said.
func (f *feed) send(ev transcript.ExportEvent) error {
	f.at = f.at.Add(time.Second)
	ev.Time = f.at
	return f.m.Write(f.meta, ev)
}

func (f *feed) must(ev transcript.ExportEvent) {
	f.t.Helper()
	if err := f.send(ev); err != nil {
		f.t.Fatal(err)
	}
}

func (f *feed) prompt(text string) {
	f.t.Helper()
	f.must(transcript.ExportEvent{Kind: transcript.ExportPrompt, Text: text})
}

func (f *feed) say(text string) {
	f.t.Helper()
	f.must(transcript.ExportEvent{Kind: transcript.ExportMessage, Text: text})
}

func (f *feed) call(tool, id string, input any) {
	f.t.Helper()
	f.must(transcript.ExportEvent{Kind: transcript.ExportToolCall, Tool: tool, ToolUseID: id, Input: input})
}

func (f *feed) result(tool, id, out string) {
	f.t.Helper()
	f.must(transcript.ExportEvent{Kind: transcript.ExportToolResult, Tool: tool, ToolUseID: id, Output: out})
}

// path is the file the feed has written to.
func (f *feed) path() string { return f.m.Path(f.meta.Conversation) }

func (f *feed) finish() { f.m.Finish(f.meta) }

func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("a line is not JSON: %v\n%s", err, sc.Text())
		}
		out = append(out, e)
	}
	return out
}

func types(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Type)
	}
	return out
}

func TestSessionFileHoldsOneObjectPerLineWithTheWhoAndWhen(t *testing.T) {
	m, dir := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("fix the build")
	f.call("Bash", "t1", map[string]any{"command": "go build ./..."})
	f.result("Bash", "t1", "ok")
	f.say("Built.")
	path := f.path()
	f.finish()

	if !strings.HasPrefix(path, filepath.Join(dir, "recordings", "shop-")) {
		t.Errorf("recording is not in the project's folder under the state dir: %s", path)
	}
	// Named for the session: when the conversation began, and which it is.
	if want := "20261001T090001Z-conv-1.jsonl"; filepath.Base(path) != want {
		t.Errorf("file is %s, want %s", filepath.Base(path), want)
	}
	es := readEntries(t, path)
	want := []string{TypeStarted, TypePrompt, TypeToolCall, TypeToolResult, TypeAssistant, TypeStopped}
	if got := types(es); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v, want %v", got, want)
	}
	for i, e := range es {
		if e.V != Version || e.Project != "shop" || e.Agent != "claude" || e.Seq != int64(i+1) {
			t.Errorf("a line lacks who it is about: %+v", e)
		}
		if e.Session != "20261001T090001Z-conv-1" {
			t.Errorf("session = %q", e.Session)
		}
		if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
			t.Errorf("time %q is not RFC 3339: %v", e.Time, err)
		}
	}
	// A transcript says nothing of how it was made.
	if es[0].Text != startText || es[len(es)-1].Text != endText {
		t.Errorf("first line %+v, last %+v", es[0], es[len(es)-1])
	}
	// The lines carry the times of the events.
	if es[0].Time != es[1].Time || es[len(es)-1].Time != es[len(es)-2].Time {
		t.Errorf("the opening and closing lines are not at the first and last event: %s %s / %s %s", es[0].Time, es[1].Time, es[len(es)-2].Time, es[len(es)-1].Time)
	}
	if in, _ := es[2].Input.(map[string]any); in["command"] != "go build ./..." {
		t.Errorf("tool input = %v", es[2].Input)
	}
	if m.Active(testMeta.Conversation) {
		t.Error("the pane still has a file open after Finish")
	}
}

// A transcript is made from a conversation, so making it again gives the same
// file rather than another.
func TestTheSameConversationMakesTheSameFile(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("one")
	a := f.path()
	f.finish()
	g := newFeed(t, m)
	g.prompt("one")
	g.prompt("two")
	if b := g.path(); a != b {
		t.Fatalf("one conversation in two files: %s and %s", a, b)
	}
	g.finish()
	if es := readEntries(t, a); len(es) != 4 {
		t.Errorf("the file was added to rather than made again: %v", types(es))
	}
}

func TestFilesAreNotReadableByOthers(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("hello")
	fi, err := os.Stat(f.path())
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no group or other bits to speak of; elsewhere they must be off.
	if perm := fi.Mode().Perm(); perm&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("transcript mode is %o", perm)
	}
}

func TestNothingIsWrittenUntilTheConversationHasAnEvent(t *testing.T) {
	m, dir := newTestManager(t)
	if m.Active(testMeta.Conversation) {
		t.Error("a pane with nothing said is open")
	}
	m.Finish(testMeta)
	if _, err := os.Stat(filepath.Join(dir, "recordings")); err == nil {
		t.Error("a folder was made for a conversation with nothing in it")
	}
}

func TestSecretsAreRedactedAndLongOutputClipped(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("use key sk-ant-api03-abcdefghijklmnopqrstuvwxyz and ghp_" + strings.Repeat("a", 36))
	f.call("Bash", "", map[string]any{"command": "curl -H 'Authorization: Bearer abcdefghijklmnopqrstuv' https://x", "env": map[string]any{"DB_PASSWORD": "hunter2"}})
	f.result("Bash", "", strings.Repeat("x", MaxFieldBytes*3))
	path := f.path()
	f.finish()

	raw, _ := os.ReadFile(path)
	for _, leak := range []string{"sk-ant-api03", "ghp_aaaa", "abcdefghijklmnopqrstuv", "hunter2"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the transcript still holds %q:\n%s", leak, raw)
		}
	}
	es := readEntries(t, path)
	out := es[3].Output
	if len(out) > MaxFieldBytes+100 || !strings.Contains(out, "[clipped ") {
		t.Errorf("a long output was not clipped with a marker (len %d)", len(out))
	}
}

func TestOutputOfASecretFileIsWithheld(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.call("Read", "r1", map[string]any{"file_path": "/work/shop/.env"})
	f.result("Read", "r1", "STRIPE=plainvalue")
	f.call("Write", "w1", map[string]any{"file_path": "deploy/id_rsa", "content": "private stuff"})
	f.call("Read", "r2", map[string]any{"file_path": "README.md"})
	f.result("Read", "r2", "plain readme")
	path := f.path()
	f.finish()

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "plainvalue") || strings.Contains(string(raw), "private stuff") {
		t.Errorf("a secret file's contents were recorded:\n%s", raw)
	}
	if !strings.Contains(string(raw), "plain readme") {
		t.Error("an ordinary file's contents were withheld too")
	}
	if !strings.Contains(string(raw), ".env") {
		t.Error("the secret file's name was dropped, which is not a secret")
	}
}

func TestSizeCapEndsTheFileWithAMarker(t *testing.T) {
	m, _ := newTestManager(t)
	m.max = 2 << 10
	f := newFeed(t, m)
	var err error
	for i := 0; i < 100 && err == nil; i++ {
		err = f.send(transcript.ExportEvent{Kind: transcript.ExportMessage, Text: strings.Repeat("y", 200)})
	}
	if !errors.Is(err, ErrFull) {
		t.Fatalf("Write never reported the cap: %v", err)
	}
	path := f.path()
	f.finish()
	fi, _ := os.Stat(path)
	if fi.Size() > 3<<10 {
		t.Errorf("file is %d bytes past a cap of %d", fi.Size(), m.max)
	}
	es := readEntries(t, path)
	last := es[len(es)-1]
	if last.Type != TypeTruncated || strings.Contains(last.Text, "recording") {
		t.Errorf("last line is %+v, want %s", last, TypeTruncated)
	}
}

func TestRetentionDropsOldFilesAndTheOldestBeyondTheCount(t *testing.T) {
	m, dir := newTestManager(t)
	folder := filepath.Join(dir, "recordings", Folder(testMeta.Project, testMeta.ProjectRoot))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	write := func(name string, age time.Duration) string {
		p := filepath.Join(folder, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write("old.jsonl", (RetainDays+1)*24*time.Hour)
	recent := write("recent.jsonl", time.Hour)
	other := write("notes.txt", (RetainDays+5)*24*time.Hour)
	for i := 0; i < RetainFiles; i++ {
		write("bulk-"+string(rune('a'+i%26))+string(rune('a'+i/26))+".jsonl", time.Duration(i+2)*time.Minute)
	}

	newFeed(t, m).prompt("hello")

	if _, err := os.Stat(old); err == nil {
		t.Error("a file past the retention age was kept")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("a file that is not a transcript was touched")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("a recent file was removed")
	}
	infos := listFolder(folder)
	if len(infos) > RetainFiles {
		t.Errorf("%d transcripts kept, want at most %d", len(infos), RetainFiles)
	}
}

func TestListFindsRecordingsNewestFirst(t *testing.T) {
	m, dir := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("one")
	a := f.path()
	f.finish()
	g := newFeed(t, m)
	g.meta.Project, g.meta.ProjectRoot, g.meta.Conversation = "blog", "/work/blog", "conv-2"
	g.prompt("two")
	b := g.path()
	g.finish()
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(a, past, past)

	got, err := List(func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != b || got[1].Path != a {
		t.Fatalf("List = %+v", got)
	}
	if got[0].Project != "blog" || got[1].Agent != "claude" {
		t.Errorf("details not read from the first line: %+v", got)
	}
	if none, err := List(func() (string, error) { return t.TempDir(), nil }); err != nil || len(none) != 0 {
		t.Errorf("an empty state dir gave %v, %v", none, err)
	}
}

func TestFolderSeparatesProjectsOfOneName(t *testing.T) {
	if Folder("shop", "/a/shop") == Folder("shop", "/b/shop") {
		t.Error("two projects called shop share a folder")
	}
	if f := Folder("../../etc", "/x"); strings.ContainsAny(f, `/\`) || strings.HasPrefix(f, ".") {
		t.Errorf("folder name %q can leave the recordings folder", f)
	}
}

func TestRedactPatterns(t *testing.T) {
	for _, secret := range []string{
		"AKIAIOSFODNN7EXAMPLE",
		"xoxb-123456789012-abcdefghij",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----",
		"postgres://admin:s3cretpw@db.internal/app",
		"export API_KEY=abc123def",
		`{"client_secret": "zzz999"}`,
		"password: correcthorse",
	} {
		got := Redact("before " + secret + " after")
		for _, frag := range []string{"AKIAIOSFODNN7EXAMPLE", "123456789012-abcdefghij", "abcdefghijklmnop", "MIIabc", "s3cretpw", "abc123def", "zzz999", "correcthorse"} {
			if strings.Contains(got, frag) {
				t.Errorf("Redact(%q) = %q, still has %q", secret, got, frag)
			}
		}
		if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
			t.Errorf("Redact(%q) = %q ate the text around the secret", secret, got)
		}
	}
	plain := "go test ./internal/record -run TestRedact && author: someone"
	if got := Redact(plain); got != plain {
		t.Errorf("Redact changed text with no secret in it: %q", got)
	}
}

// A quoted secret value is redacted whatever letters it holds. The quoted-value
// pattern used to be written so that a value containing the letter n (or, under
// the case-insensitive flag, N) broke the match and the secret was written out
// in full -- and a real token or password almost always has an n in it.
func TestRedactsQuotedValuesContainingN(t *testing.T) {
	for _, line := range []string{
		`"password":"hunterN2incredible"`,
		`{"api_key": "ANiceLongApiKeyValue"}`,
		`token = "another-secret-one"`,
		`"client_secret":"navigatorKey"`,
		`'password':'contains an n'`,
	} {
		got := Redact(line)
		for _, frag := range []string{"hunterN2incredible", "ANiceLongApiKeyValue", "another-secret-one", "navigatorKey", "contains an n"} {
			if strings.Contains(got, frag) {
				t.Errorf("Redact(%q) = %q, left the secret value in", line, got)
			}
		}
	}
	// An escaped quote inside the value must not end the value early and leak
	// what follows it.
	got := Redact(`"password":"a\"b secret n tail"`)
	if strings.Contains(got, "tail") {
		t.Errorf("Redact left the tail of a value with an escaped quote: %q", got)
	}
}

func TestClipKeepsRunesWhole(t *testing.T) {
	s := strings.Repeat("é", 10)
	got := Clip(s, 5)
	if !strings.Contains(got, "[clipped ") {
		t.Fatalf("no marker: %q", got)
	}
	if body, _, _ := strings.Cut(got, "…"); strings.ContainsRune(body, '\uFFFD') || len(body)%2 != 0 {
		t.Errorf("a rune was cut in half: %q", got)
	}
}
