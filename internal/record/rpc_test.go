package record

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newRPC(t *testing.T, cfg RPCConfig) (*RPCRecorder, *Manager, string) {
	t.Helper()
	m, dir := newTestManager(t)
	if cfg.Meta.Project == "" {
		cfg.Meta = Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "opencode", Conversation: "conv-1234567890"}
	}
	if cfg.Now == nil {
		clk := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
		cfg.Now = func() time.Time { clk = clk.Add(time.Millisecond); return clk }
	}
	if cfg.Interval == 0 {
		cfg.Interval = time.Hour // tests flush by hand
	}
	r, err := m.StartRPC(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, m, dir
}

func chunk(kind, sid, text string) []byte {
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":%q,"update":{"sessionUpdate":%q,"content":{"type":"text","text":%q}}}}`, sid, kind, text))
}

func toolUpd(sid, id, status string, extra string) []byte {
	return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":%q,"update":{"sessionUpdate":"tool_call_update","toolCallId":%q,"status":%q%s}}}`, sid, id, status, extra))
}

func read(t *testing.T, r *RPCRecorder) RPCFile {
	t.Helper()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := ReadRPCFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func at(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func msgs(f RPCFile) []RPCLine {
	var out []RPCLine
	for _, l := range f.Lines {
		if l.Type == TypeRPCMessage {
			out = append(out, l)
		}
	}
	return out
}

func TestRPCRoundTrip(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`))
	r.Tap(RPCIn, []byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{}}}`))
	r.Tap(RPCIn, []byte(`{"jsonrpc":"2.0","id":12345678901234567890,"method":"session/request_permission","params":{}}`))
	f := read(t, r)
	if f.Damaged != 0 || f.TornTail {
		t.Fatalf("damaged %d, torn %v", f.Damaged, f.TornTail)
	}
	if len(f.Lines) != 5 || f.Lines[0].Type != TypeRPCStarted || f.Lines[4].Type != TypeRPCStopped {
		t.Fatalf("lines = %+v", f.Lines)
	}
	for i, l := range f.Lines {
		if l.V != RPCVersion || l.Seq != int64(i+1) || l.Session == "" || l.Time == "" {
			t.Errorf("line %d = %+v", i, l)
		}
	}
	m := msgs(f)
	if m[0].Dir != RPCOut || m[1].Dir != RPCIn || !at(m[0].Time).Before(at(m[1].Time)) {
		t.Errorf("messages = %+v", m)
	}
	var got map[string]any
	if err := json.Unmarshal(m[0].Msg, &got); err != nil || got["method"] != "initialize" {
		t.Errorf("msg = %s", m[0].Msg)
	}
	// An id too big for a float comes back exactly.
	if !strings.Contains(string(m[2].Msg), "12345678901234567890") {
		t.Errorf("id changed: %s", m[2].Msg)
	}
	if f.Lines[0].Project != "demo" || f.Lines[0].Agent != "opencode" {
		t.Errorf("header = %+v", f.Lines[0])
	}
}

func TestRPCFileIsPrivateAndBesideTheTerminalRecordings(t *testing.T) {
	r, m, dir := newRPC(t, RPCConfig{})
	_ = r.Flush()
	want := filepath.Join(dir, recordingsDir, Folder("demo", filepath.Join(dir, "demo")))
	if filepath.Dir(r.Path()) != want || !IsRPC(r.Path()) {
		t.Errorf("path = %s, want in %s", r.Path(), want)
	}
	if fi, err := os.Stat(r.Path()); err != nil || (fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Errorf("stat = %v %v", fi, err)
	}
	// A second recording of the same second does not overwrite the first.
	r2, err := m.StartRPC(RPCConfig{Meta: Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "opencode", Conversation: "conv-1234567890"}, Now: func() time.Time {
		return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := m.StartRPC(RPCConfig{Meta: Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "opencode", Conversation: "conv-1234567890"}, Now: func() time.Time {
		return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	defer r3.Close()
	if r2.Path() == r3.Path() {
		t.Error("two recordings share a file")
	}
}

func TestRPCCoalescesChunksOfOneKind(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	for _, p := range []string{"Hel", "lo ", "wor", "ld"} {
		r.Tap(RPCIn, chunk("agent_message_chunk", "s1", p))
	}
	r.Tap(RPCIn, chunk("agent_thought_chunk", "s1", "hmm"))
	r.Tap(RPCIn, chunk("agent_thought_chunk", "s1", "m"))
	r.Tap(RPCIn, chunk("agent_thought_chunk", "s2", "other session"))
	m := msgs(read(t, r))
	if len(m) != 3 {
		t.Fatalf("%d messages: %+v", len(m), m)
	}
	var first struct {
		Params struct {
			Update struct {
				Content struct{ Text string }
			}
		}
	}
	if err := json.Unmarshal(m[0].Msg, &first); err != nil || first.Params.Update.Content.Text != "Hello world" {
		t.Errorf("first = %s", m[0].Msg)
	}
	if m[0].Merged != 4 || m[0].Until == "" || !at(m[0].Until).After(at(m[0].Time)) || m[1].Merged != 2 || m[2].Merged != 0 {
		t.Errorf("merged %d %d %d, time %s until %s", m[0].Merged, m[1].Merged, m[2].Merged, m[0].Time, m[0].Until)
	}
}

func TestRPCAnythingElseBetweenChunksKeepsTheOrder(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "a"))
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}`))
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "b"))
	m := msgs(read(t, r))
	if len(m) != 3 || m[0].Merged != 0 || m[1].Dir != RPCOut || m[2].Merged != 0 {
		t.Errorf("messages = %+v", m)
	}
}

func TestRPCToolUpdatesMergeAndLaterFieldsWin(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, toolUpd("s1", "c1", "in_progress", `,"title":"Run ls"`))
	r.Tap(RPCIn, toolUpd("s1", "c1", "in_progress", `,"content":[{"type":"content","content":{"type":"text","text":"a"}}]`))
	r.Tap(RPCIn, toolUpd("s1", "c1", "completed", `,"content":[{"type":"content","content":{"type":"text","text":"ab"}}]`))
	r.Tap(RPCIn, toolUpd("s1", "c2", "completed", ""))
	m := msgs(read(t, r))
	if len(m) != 2 || m[0].Merged != 3 {
		t.Fatalf("messages = %+v", m)
	}
	s := string(m[0].Msg)
	if !strings.Contains(s, `"title":"Run ls"`) || !strings.Contains(s, `"status":"completed"`) || !strings.Contains(s, `"text":"ab"`) || strings.Contains(s, `"text":"a"`) {
		t.Errorf("merged = %s", s)
	}
}

func TestRPCAHugeRunOfChunksIsSeveralLines(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	piece := strings.Repeat("w", 1000)
	for i := 0; i < 100; i++ {
		r.Tap(RPCIn, chunk("agent_message_chunk", "s1", piece))
	}
	m := msgs(read(t, r))
	total := 0
	for _, l := range m {
		var u struct {
			Params struct {
				Update struct{ Content struct{ Text string } }
			}
		}
		_ = json.Unmarshal(l.Msg, &u)
		total += len(u.Params.Update.Content.Text)
		if len(u.Params.Update.Content.Text) > MaxMessageBytes+100 {
			t.Errorf("a line holds %d bytes of text", len(u.Params.Update.Content.Text))
		}
	}
	if len(m) < 3 || total != 100000 {
		t.Errorf("%d lines, %d bytes of text", len(m), total)
	}
}

func TestRPCTheTimerWritesWhatIsHeld(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{Interval: 20 * time.Millisecond})
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "held"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, _ := ReadRPCFile(r.Path())
		if len(msgs(f)) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a held message never reached the file")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRPCLargeEntriesAreClipped(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	big := strings.Repeat("x", 5<<20)
	r.Tap(RPCIn, toolUpd("s1", "c1", "completed", `,"content":[{"type":"content","content":{"type":"text","text":"`+big+`"}}]`))
	// A prompt is a person's words and has the larger limit.
	prompt := strings.Repeat("p", 20<<10)
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s1","prompt":[{"type":"text","text":"`+prompt+`"}]}}`))
	// Over the limit for parsing at all.
	r.Tap(RPCIn, []byte(`{"jsonrpc":"2.0","method":"x","params":"`+strings.Repeat("y", rpcMaxRaw)+`"}`))
	f := read(t, r)
	m := msgs(f)
	if len(m) != 3 {
		t.Fatalf("messages = %d", len(m))
	}
	if len(m[0].Msg) > 9<<10 || m[0].Clipped != 1 || !strings.Contains(string(m[0].Msg), "clipped") {
		t.Errorf("tool line: %d bytes, clipped %d", len(m[0].Msg), m[0].Clipped)
	}
	if len(m[1].Msg) < 20<<10 || m[1].Clipped != 0 {
		t.Errorf("prompt line: %d bytes, clipped %d", len(m[1].Msg), m[1].Clipped)
	}
	if m[2].Msg != nil || !strings.Contains(m[2].Text, "not recorded") {
		t.Errorf("oversize line = %+v", m[2])
	}
}

func TestRPCNotJSONIsKeptClipped(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, []byte("this is not json"))
	r.Tap(RPCIn, []byte(`[1,2,3]`))
	r.Tap(RPCIn, []byte(`{"a":1} trailing`))
	m := msgs(read(t, r))
	if len(m) != 3 || m[0].Raw != "this is not json" || m[1].Raw != "[1,2,3]" || m[2].Raw != `{"a":1} trailing` {
		t.Errorf("messages = %+v", m)
	}
}

func TestRPCSecretsAreRedactedEvenWhenSplitAcrossChunks(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	key := "ghp_" + strings.Repeat("a1B2", 9)
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "my token is "+key[:12]))
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", key[12:]+" ok"))
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":3,"method":"authenticate","params":{"methodId":"k","apiKey":"hunter2hunter2"}}`))
	f := read(t, r)
	raw, _ := os.ReadFile(r.Path())
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), "hunter2") {
		t.Fatalf("a secret reached the file:\n%s", raw)
	}
	m := msgs(f)
	if !m[0].Redacted || !m[1].Redacted {
		t.Errorf("redacted flags = %v %v", m[0].Redacted, m[1].Redacted)
	}
}

func TestRPCHookCanClipOrDropAndAPanicDrops(t *testing.T) {
	calls := 0
	r, _, _ := newRPC(t, RPCConfig{Hook: func(dir string, msg map[string]any) bool {
		calls++
		switch msg["method"] {
		case "secret/method":
			return false
		case "panic":
			panic("boom")
		case "clip":
			delete(msg, "params")
		}
		return true
	}})
	r.Tap(RPCOut, []byte(`{"method":"secret/method"}`))
	r.Tap(RPCOut, []byte(`{"method":"panic"}`))
	r.Tap(RPCOut, []byte(`{"method":"clip","params":{"a":1}}`))
	r.Tap(RPCOut, []byte(`{"method":"keep"}`))
	m := msgs(read(t, r))
	if len(m) != 2 || strings.Contains(string(m[0].Msg), "params") || !strings.Contains(string(m[1].Msg), "keep") {
		t.Errorf("messages = %+v", m)
	}
	if calls != 4 {
		t.Errorf("hook ran %d times", calls)
	}
}

func TestRPCCapDropsTheOldestLines(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{MaxBytes: 16 << 10})
	for i := 0; i < 400; i++ {
		r.Tap(RPCOut, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"m","params":{"pad":"%s"}}`, i, strings.Repeat("z", 100))))
		if i%50 == 0 {
			_ = r.Flush()
			if fi, err := os.Stat(r.Path()); err == nil && fi.Size() > 16<<10 {
				t.Fatalf("file is %d bytes after %d messages", fi.Size(), i+1)
			}
		}
	}
	f := read(t, r)
	if fi, _ := os.Stat(r.Path()); fi.Size() > 16<<10 {
		t.Fatalf("file is %d bytes", fi.Size())
	}
	if f.Damaged != 0 || f.Lines[0].Type != TypeRPCStarted || f.Lines[1].Type != TypeRPCDropped || f.Lines[len(f.Lines)-1].Type != TypeRPCStopped {
		t.Fatalf("shape: damaged %d, first %s, second %s", f.Damaged, f.Lines[0].Type, f.Lines[1].Type)
	}
	m := msgs(f)
	if !strings.Contains(string(m[len(m)-1].Msg), `"id":399`) {
		t.Errorf("the newest message is gone: %s", m[len(m)-1].Msg)
	}
	d := f.Lines[1]
	if d.Dropped < 100 || d.Dropped+int64(len(m)) < 400 {
		t.Errorf("dropped %d, kept %d", d.Dropped, len(m))
	}
	// Numbers rise through the dropped note and never repeat.
	for i := 1; i < len(f.Lines); i++ {
		if f.Lines[i].Seq <= f.Lines[i-1].Seq {
			t.Fatalf("seq %d after %d at line %d", f.Lines[i].Seq, f.Lines[i-1].Seq, i)
		}
	}
}

func TestRPCAnOversizeMessageIsANoteNotAFile(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{MaxBytes: 16 << 10})
	var b strings.Builder
	b.WriteString(`{"method":"m","params":[`)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + strings.Repeat("q", 200) + `"`)
	}
	b.WriteString(`]}`)
	r.Tap(RPCIn, []byte(b.String()))
	f := read(t, r)
	m := msgs(f)
	if len(m) != 1 || m[0].Msg != nil || !strings.Contains(m[0].Text, "not recorded") {
		t.Errorf("messages = %+v", m)
	}
}

func TestRPCADamagedTailLineIsSkippedAndCounted(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, []byte(`{"id":1,"result":{}}`))
	r.Tap(RPCIn, []byte(`{"id":2,"result":{}}`))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(r.Path())
	// A crash in the middle of a line, with no stopped line after it.
	cut := strings.LastIndex(strings.TrimRight(string(raw), "\n"), "\n")
	torn := string(raw[:cut+1]) + string(raw[cut+1:cut+30])
	f, err := ReadRPC(strings.NewReader(torn))
	if err != nil || !f.TornTail || f.Damaged != 1 {
		t.Fatalf("err %v torn %v damaged %d", err, f.TornTail, f.Damaged)
	}
	if len(msgs(f)) != 2 {
		t.Errorf("the good lines were lost: %d", len(msgs(f)))
	}
	// Damage in the middle is skipped too.
	lines := strings.SplitAfter(string(raw), "\n")
	lines[2] = "{garbage\n"
	f, err = ReadRPC(strings.NewReader(strings.Join(lines, "")))
	if err != nil || f.Damaged != 1 || f.TornTail || len(f.Lines) != len(lines)-2 {
		t.Errorf("err %v damaged %d torn %v lines %d (of %d)", err, f.Damaged, f.TornTail, len(f.Lines), len(lines)-1)
	}
	// A last line that is whole JSON but has no newline is kept, as the tail.
	f, _ = ReadRPC(strings.NewReader(strings.TrimRight(string(raw), "\n")))
	if !f.TornTail || f.Damaged != 0 || len(f.Lines) != 4 {
		t.Errorf("torn %v damaged %d lines %d", f.TornTail, f.Damaged, len(f.Lines))
	}
	// A line longer than the reader takes is damage, not memory.
	f, err = ReadRPC(strings.NewReader(strings.Repeat("a", MaxRPCLineBytes+10) + "\n" + string(raw)))
	if err != nil || f.Damaged != 1 || len(f.Lines) != 4 {
		t.Errorf("err %v damaged %d lines %d", err, f.Damaged, len(f.Lines))
	}
}

func TestRPCSharesTheTerminalRetention(t *testing.T) {
	m, dir := newTestManager(t)
	meta := Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "a", Conversation: "c-1"}
	folder := filepath.Join(dir, recordingsDir, Folder(meta.Project, meta.ProjectRoot))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(folder, "old.rpc.jsonl")
	oldTerm := filepath.Join(folder, "old.jsonl")
	fresh := filepath.Join(folder, "fresh.rpc.jsonl")
	for _, p := range []string{old, oldTerm, fresh} {
		if err := os.WriteFile(p, []byte(`{"v":1}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	when := time.Now().Add(-(RetainDays + 1) * 24 * time.Hour)
	for _, p := range []string{old, oldTerm} {
		_ = os.Chtimes(p, when, when)
	}
	r, err := m.StartRPC(RPCConfig{Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, p := range []string{old, oldTerm} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s is past the retention and was kept", filepath.Base(p))
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a recent file was removed: %v", err)
	}
	if _, err := os.Stat(r.Path()); err != nil {
		t.Errorf("the new recording is gone: %v", err)
	}
}

func TestRPCNothingIsWrittenAfterClose(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, []byte(`{"id":1}`))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(r.Path())
	r.Tap(RPCIn, []byte(`{"id":2}`))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(r.Path())
	if string(before) != string(after) {
		t.Error("the file changed after Close")
	}
}
