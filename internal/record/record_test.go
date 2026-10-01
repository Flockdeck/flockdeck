package record

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

var testMeta = Meta{Pane: "0123456789abcdef", PaneName: "api", Project: "shop", ProjectRoot: "/work/shop", Agent: "claude", Model: "opus", Conversation: "conv-1"}

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
	path, err := m.Start(testMeta, "turned on")
	if err != nil {
		t.Fatal(err)
	}
	m.Record(testMeta, Entry{Type: TypePrompt, Text: "fix the build"})
	m.Record(testMeta, Entry{Type: TypeToolCall, Tool: "Bash", ToolUseID: "t1", Input: map[string]any{"command": "go build ./..."}})
	m.Record(testMeta, Entry{Type: TypeToolResult, Tool: "Bash", ToolUseID: "t1", Output: "ok"})
	m.Record(testMeta, Entry{Type: TypeStatus, Status: "idle", Previous: "working"})
	m.Stop(testMeta, "turned off")

	if !strings.HasPrefix(path, filepath.Join(dir, "recordings", "shop-")) {
		t.Errorf("recording is not in the project's folder under the state dir: %s", path)
	}
	es := readEntries(t, path)
	want := []string{TypeStarted, TypePrompt, TypeToolCall, TypeToolResult, TypeStatus, TypeStopped}
	if got := types(es); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v, want %v", got, want)
	}
	for _, e := range es {
		if e.V != Version || e.Pane != testMeta.Pane || e.PaneName != "api" || e.Project != "shop" || e.Agent != "claude" || e.Model != "opus" {
			t.Errorf("a line lacks who it is about: %+v", e)
		}
		if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
			t.Errorf("time %q is not RFC 3339: %v", e.Time, err)
		}
	}
	if in, _ := es[2].Input.(map[string]any); in["command"] != "go build ./..." {
		t.Errorf("tool input = %v", es[2].Input)
	}
	if m.Active(testMeta.Pane) {
		t.Error("the pane is still recording after Stop")
	}
}

func TestEachSessionGetsItsOwnFile(t *testing.T) {
	m, _ := newTestManager(t)
	a, _ := m.Start(testMeta, "turned on")
	m.Stop(testMeta, "turned off")
	b, _ := m.Start(testMeta, "turned on")
	m.Stop(testMeta, "turned off")
	if a == b {
		t.Fatalf("two sessions share %s", a)
	}
}

func TestFilesAreNotReadableByOthers(t *testing.T) {
	m, _ := newTestManager(t)
	path, _ := m.Start(testMeta, "turned on")
	defer m.Stop(testMeta, "turned off")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no group or other bits to speak of; elsewhere they must be off.
	if perm := fi.Mode().Perm(); perm&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("transcript mode is %o", perm)
	}
}

func TestRecordingStartsItselfForAPaneRestoredWhileRecording(t *testing.T) {
	m, _ := newTestManager(t)
	m.Record(testMeta, Entry{Type: TypePrompt, Text: "hello"})
	path := m.Path(testMeta.Pane)
	if path == "" {
		t.Fatal("no file was opened")
	}
	es := readEntries(t, path)
	if len(es) < 2 || es[0].Type != TypeStarted || es[0].Text != "resumed" {
		t.Errorf("entries = %v", types(es))
	}
}

func TestSecretsAreRedactedAndLongOutputClipped(t *testing.T) {
	m, _ := newTestManager(t)
	path, _ := m.Start(testMeta, "turned on")
	m.Record(testMeta, Entry{Type: TypePrompt, Text: "use key sk-ant-api03-abcdefghijklmnopqrstuvwxyz and ghp_" + strings.Repeat("a", 36)})
	m.Record(testMeta, Entry{Type: TypeToolCall, Tool: "Bash", Input: map[string]any{"command": "curl -H 'Authorization: Bearer abcdefghijklmnopqrstuv' https://x", "env": map[string]any{"DB_PASSWORD": "hunter2"}}})
	m.Record(testMeta, Entry{Type: TypeToolResult, Tool: "Bash", Output: strings.Repeat("x", MaxFieldBytes*3)})
	m.Stop(testMeta, "turned off")

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
	path, _ := m.Start(testMeta, "turned on")
	m.Record(testMeta, Entry{Type: TypeToolCall, Tool: "Read", ToolUseID: "r1", Input: map[string]any{"file_path": "/work/shop/.env"}})
	m.Record(testMeta, Entry{Type: TypeToolResult, Tool: "Read", ToolUseID: "r1", Output: "STRIPE=plainvalue"})
	m.Record(testMeta, Entry{Type: TypeToolCall, Tool: "Write", ToolUseID: "w1", Input: map[string]any{"file_path": "deploy/id_rsa", "content": "private stuff"}})
	m.Record(testMeta, Entry{Type: TypeToolCall, Tool: "Read", ToolUseID: "r2", Input: map[string]any{"file_path": "README.md"}})
	m.Record(testMeta, Entry{Type: TypeToolResult, Tool: "Read", ToolUseID: "r2", Output: "plain readme"})
	m.Stop(testMeta, "turned off")

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

func TestPermissionOutcomesAreInferredFromWhatHappensNext(t *testing.T) {
	m, _ := newTestManager(t)
	path, _ := m.Start(testMeta, "turned on")
	// Asked, then the tool ran: allowed.
	m.Record(testMeta, Entry{Type: TypePermission, Tool: "Bash"})
	m.Record(testMeta, Entry{Type: TypeToolResult, Tool: "Bash", Output: "done"})
	// Asked, then the user moved on to a new prompt: denied.
	m.Record(testMeta, Entry{Type: TypePermission, Tool: "Edit"})
	m.Record(testMeta, Entry{Type: TypePrompt, Text: "no, do it differently"})
	// Auto-approved is reported as it is, and is not inferred.
	m.Record(testMeta, Entry{Type: TypeOutcome, Tool: "Bash", Outcome: OutcomeAutoApproved, Reason: "read-only"})
	m.Stop(testMeta, "turned off")

	var outcomes []Entry
	for _, e := range readEntries(t, path) {
		if e.Type == TypeOutcome {
			outcomes = append(outcomes, e)
		}
	}
	if len(outcomes) != 3 {
		t.Fatalf("got %d outcomes: %+v", len(outcomes), outcomes)
	}
	if outcomes[0].Outcome != OutcomeAllowed || !outcomes[0].Inferred {
		t.Errorf("first = %+v", outcomes[0])
	}
	if outcomes[1].Outcome != OutcomeDenied || !outcomes[1].Inferred {
		t.Errorf("second = %+v", outcomes[1])
	}
	if outcomes[2].Outcome != OutcomeAutoApproved || outcomes[2].Inferred {
		t.Errorf("third = %+v", outcomes[2])
	}
}

func TestSizeCapEndsTheFileWithAMarkerAndTheRecording(t *testing.T) {
	m, _ := newTestManager(t)
	m.max = 2 << 10
	path, _ := m.Start(testMeta, "turned on")
	ok := true
	for i := 0; i < 100 && ok; i++ {
		ok = m.Record(testMeta, Entry{Type: TypeAssistant, Text: strings.Repeat("y", 200)})
	}
	if ok {
		t.Fatal("Record never reported the cap")
	}
	m.Stop(testMeta, "size cap reached")
	fi, _ := os.Stat(path)
	if fi.Size() > 3<<10 {
		t.Errorf("file is %d bytes past a cap of %d", fi.Size(), m.max)
	}
	es := readEntries(t, path)
	if last := es[len(es)-1]; last.Type != TypeTruncated {
		t.Errorf("last line is %s, want %s", last.Type, TypeTruncated)
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

	if _, err := m.Start(testMeta, "turned on"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(testMeta, "turned off")

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
	a, _ := m.Start(testMeta, "turned on")
	m.Stop(testMeta, "turned off")
	other := testMeta
	other.Pane, other.Project, other.ProjectRoot = "ffffffffffff", "blog", "/work/blog"
	b, _ := m.Start(other, "turned on")
	m.Stop(other, "turned off")
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(a, past, past)

	got, err := List(func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != b || got[1].Path != a {
		t.Fatalf("List = %+v", got)
	}
	if got[0].Project != "blog" || got[1].PaneName != "api" || got[1].Agent != "claude" {
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
