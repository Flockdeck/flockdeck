package record

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// chunkTexts is the text of every chunk line of a recording, joined by kind and
// session in the order written, and the number of messages each stands for.
func chunkTexts(f RPCFile) (texts map[string]string, merged map[string]int) {
	texts, merged = map[string]string{}, map[string]int{}
	for _, l := range msgs(f) {
		var m struct {
			Params struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					Kind    string `json:"sessionUpdate"`
					Content struct{ Text string }
				}
			}
		}
		if json.Unmarshal(l.Msg, &m) != nil || !strings.HasSuffix(m.Params.Update.Kind, "_chunk") {
			continue
		}
		k := m.Params.Update.Kind + "/" + m.Params.SessionID
		texts[k] += m.Params.Update.Content.Text
		n := l.Merged
		if n == 0 {
			n = 1
		}
		merged[k] += n
	}
	return texts, merged
}

func TestRPCCoalescesChunksOfOneKind(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	for _, p := range []string{"Hel", "lo ", "wor", "ld"} {
		r.Tap(RPCIn, chunk("agent_message_chunk", "s1", p))
	}
	r.Tap(RPCIn, chunk("agent_thought_chunk", "s1", "hmm"))
	r.Tap(RPCIn, chunk("agent_thought_chunk", "s1", "m"))
	r.Tap(RPCIn, chunk("agent_thought_chunk", "s2", "other session"))
	f := read(t, r)
	texts, merged := chunkTexts(f)
	if texts["agent_message_chunk/s1"] != "Hello world" || texts["agent_thought_chunk/s1"] != "hmmm" || texts["agent_thought_chunk/s2"] != "other session" {
		t.Errorf("texts = %q", texts)
	}
	if merged["agent_message_chunk/s1"] != 4 || merged["agent_thought_chunk/s1"] != 2 || merged["agent_thought_chunk/s2"] != 1 {
		t.Errorf("merged = %v", merged)
	}
	if n := len(msgs(f)); n != 3 {
		t.Errorf("%d lines for three runs of text", n)
	}
	for _, l := range msgs(f) {
		if l.Merged > 1 && !at(l.Until).After(at(l.Time)) {
			t.Errorf("until %s is not after time %s", l.Until, l.Time)
		}
	}
}

func TestRPCAnythingElseBetweenChunksIsWrittenInOrder(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "a"))
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}`))
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "b"))
	f := read(t, r)
	m := msgs(f)
	// The end of a text waits for the text after it, so the cancel is written
	// first; the true times are on the lines.
	if len(m) != 2 || m[0].Dir != RPCOut || m[1].Merged != 2 {
		t.Fatalf("messages = %+v", m)
	}
	if texts, _ := chunkTexts(f); texts["agent_message_chunk/s1"] != "ab" {
		t.Errorf("texts = %q", texts)
	}
	if !at(m[1].Time).Before(at(m[0].Time)) {
		t.Errorf("the chunk (%s) was seen before the cancel (%s)", m[1].Time, m[0].Time)
	}
}

func TestRPCTheTimerWritesAllButTheEndOfWhatIsHeld(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{Interval: 20 * time.Millisecond})
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "one two three four five"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(r.Path())
		if strings.Contains(string(raw), "one two three ") {
			if strings.Contains(string(raw), "five") {
				t.Fatalf("the end of the text was written before more arrived:\n%s", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the held text never reached the file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f := read(t, r)
	if texts, _ := chunkTexts(f); texts["agent_message_chunk/s1"] != "one two three four five" {
		t.Errorf("texts = %q", texts)
	}
}

// secretKeys are shapes the redaction patterns know.
func secretKeys() map[string]string {
	return map[string]string{
		"sk-ant": "sk-ant-api03-" + strings.Repeat("aB3dE5gH7j", 8),
		"ghp":    "ghp_" + strings.Repeat("a1B2", 9),
	}
}

// noFragment fails if any run of 10 bytes of secret is in the file.
func noFragment(t *testing.T, what, file, secret string) {
	t.Helper()
	for i := 0; i+10 <= len(secret); i++ {
		if strings.Contains(file, secret[i:i+10]) {
			t.Errorf("%s: %q of the secret reached the file", what, secret[i:i+10])
			return
		}
	}
}

// A secret that begins in one chunk and ends in the next is not on the disk,
// whether the first chunk was written between them by the timer, because the
// line was full, or because another message came in the middle.
func TestRPCSecretsSplitAcrossLinesAreNeverWritten(t *testing.T) {
	for name, secret := range secretKeys() {
		for _, cut := range []int{8, 12, 39} {
			head, tail := secret[:cut], secret[cut:]
			shapes := map[string]func(r *RPCRecorder){
				"flushed between": func(r *RPCRecorder) {
					r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "my key is "+head))
					_ = r.Flush()
					r.Tap(RPCIn, chunk("agent_message_chunk", "s1", tail+" and that is all"))
				},
				"line full": func(r *RPCRecorder) {
					filler := strings.Repeat("word ", (MaxMessageBytes-len(head)-20)/5)
					r.Tap(RPCIn, chunk("agent_message_chunk", "s1", filler+head))
					r.Tap(RPCIn, chunk("agent_message_chunk", "s1", tail+" end"))
				},
				"other message between": func(r *RPCRecorder) {
					r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "my key is "+head))
					r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s1"}}`))
					r.Tap(RPCIn, chunk("agent_message_chunk", "s1", tail+" and that is all"))
				},
			}
			for shape, run := range shapes {
				r, _, _ := newRPC(t, RPCConfig{})
				run(r)
				f := read(t, r)
				raw, _ := os.ReadFile(r.Path())
				noFragment(t, fmt.Sprintf("%s/%d/%s", name, cut, shape), string(raw), secret)
				if !strings.Contains(string(raw), Redacted) {
					t.Errorf("%s/%d/%s: nothing was redacted:\n%.300s", name, cut, shape, raw)
				}
				if f.Damaged != 0 {
					t.Errorf("damaged = %d", f.Damaged)
				}
			}
		}
	}
}

// What names a secret and what it holds are kept together even with a space
// between them.
func TestRPCAKeyNameAndItsValueAreHeldTogether(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "some words here and then the password: "))
	_ = r.Flush()
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "hunter2hunter2 and more"))
	f := read(t, r)
	raw, _ := os.ReadFile(r.Path())
	if strings.Contains(string(raw), "hunter2") {
		t.Errorf("the value reached the file:\n%s", raw)
	}
	if texts, _ := chunkTexts(f); !strings.HasPrefix(texts["agent_message_chunk/s1"], "some words here and then") {
		t.Errorf("texts = %q", texts)
	}
}

func TestSplitHeldKeepsTheLastTwoWordsAndNeverMoreThanTheLimit(t *testing.T) {
	cases := map[string][2]string{
		"":                       {"", ""},
		"one":                    {"", "one"},
		"one two":                {"", "one two"},
		"one two three":          {"one ", "two three"},
		"one two three ":         {"one ", "two three "},
		"one two  three\n\n":     {"one ", "two  three\n\n"},
		strings.Repeat("x", 300): {strings.Repeat("x", 44), strings.Repeat("x", 256)},
	}
	for in, want := range cases {
		h, tl := splitHeld(in)
		if h != want[0] || tl != want[1] {
			t.Errorf("splitHeld(%.20q...) = %.20q, %.20q; want %.20q, %.20q", in, h, tl, want[0], want[1])
		}
		if h+tl != in {
			t.Errorf("splitHeld lost text for %.20q", in)
		}
	}
	// Cut on a rune boundary.
	in := strings.Repeat("é", 300)
	h, tl := splitHeld(in)
	if h+tl != in || len(tl) > rpcHoldBack+1 || strings.ContainsRune(h, '�') || strings.ContainsRune(tl, '�') {
		t.Errorf("a rune was cut: %d + %d bytes", len(h), len(tl))
	}
}

func TestRPCSecretsUsedAsKeysAreRedacted(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	sk, ghp := secretKeys()["sk-ant"], secretKeys()["ghp"]
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":1,"method":"m","params":{"`+sk+`":"v","`+ghp+`":"w","nested":[{"`+ghp+`":1}],"fine":"x","`+strings.Repeat("k", 1000)+`":"long"}}`))
	f := read(t, r)
	raw, _ := os.ReadFile(r.Path())
	noFragment(t, "sk key", string(raw), sk)
	noFragment(t, "ghp key", string(raw), ghp)
	m := msgs(f)[0]
	if !m.Redacted || m.Clipped != 1 {
		t.Errorf("redacted %v clipped %d", m.Redacted, m.Clipped)
	}
	var got struct{ Params map[string]any }
	if err := json.Unmarshal(m.Msg, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Params[Redacted]; !ok || got.Params["fine"] != "x" || len(got.Params) != 5 {
		t.Errorf("params = %v", got.Params)
	}
	if _, ok := got.Params[Redacted+"#2"]; !ok {
		t.Errorf("the second redacted key was merged into the first: %v", got.Params)
	}
	for k := range got.Params {
		if len(k) > 300 {
			t.Errorf("a key of %d bytes", len(k))
		}
	}
}

func TestRPCLargeEntriesAreClipped(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	big := strings.Repeat("x", 600<<10)
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

// Lines that are small enough to read but would cost a great deal to parse are
// a note, and the recorder does not allocate for them.
func TestRPCInputsThatAreCostlyToParseAreNotParsed(t *testing.T) {
	numbers := "[" + strings.Repeat("1,", 4<<20) + "1]" // 8 MiB
	objects := "[" + strings.Repeat("{},", 2<<20) + "{}]"
	small := "[" + strings.Repeat("1,", 400000) + "1]" // 800 KiB, under rpcMaxRaw, 400,000 elements
	for name, in := range map[string]string{"8 MiB of numbers": numbers, "8 MiB of objects": objects, "800 KiB of numbers": small} {
		r, _, _ := newRPC(t, RPCConfig{})
		raw := []byte(`{"jsonrpc":"2.0","method":"m","params":` + in + `}`)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		r.Tap(RPCIn, raw)
		runtime.ReadMemStats(&after)
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
			t.Errorf("%s: %d MiB allocated for one message", name, grew>>20)
		}
		m := msgs(read(t, r))
		if len(m) != 1 || m[0].Msg != nil || !strings.Contains(m[0].Text, "not recorded") {
			t.Errorf("%s: messages = %+v", name, m)
		}
	}
	// A string with many commas in it is one element, not many.
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, []byte(`{"method":"m","params":"`+strings.Repeat("a,b,", 100000)+`"}`))
	if m := msgs(read(t, r)); len(m) != 1 || m[0].Msg == nil {
		t.Errorf("commas in a string counted: %+v", m)
	}
}

func TestRPCNothingIsOpenedAfterTheManagerIsClosed(t *testing.T) {
	m, dir := newTestManager(t)
	meta := Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "a", Conversation: "c-1"}
	open, err := m.StartRPC(RPCConfig{Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	open.Tap(RPCIn, []byte(`{"id":1}`))
	m.Close()
	// Closing the manager closes what it opened, with the closing line.
	f, err := ReadRPCFile(open.Path())
	if err != nil || len(f.Lines) == 0 || f.Lines[len(f.Lines)-1].Type != TypeRPCStopped {
		t.Errorf("open recording after Close: %v %+v", err, f.Lines)
	}
	if _, err := m.StartRPC(RPCConfig{Meta: meta}); err == nil {
		t.Fatal("a recording was opened after Close")
	}
	files, _ := filepath.Glob(filepath.Join(dir, recordingsDir, "*", "*"))
	if len(files) != 1 {
		t.Errorf("files after Close: %v", files)
	}
}

func TestRPCRotationLeavesNoTempAndStrayOnesAreSwept(t *testing.T) {
	m, dir := newTestManager(t)
	meta := Meta{Project: "demo", ProjectRoot: filepath.Join(dir, "demo"), Agent: "a", Conversation: "c-1"}
	folder := filepath.Join(dir, recordingsDir, Folder(meta.Project, meta.ProjectRoot))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(folder, "20260101T000000Z-x.rpc.jsonl.0123456789ab.new")
	if err := os.WriteFile(stray, []byte("left by a crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(stray, old, old)
	r, err := m.StartRPC(RPCConfig{Meta: meta, MaxBytes: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("a stray temp file from a crash was not swept")
	}
	for i := 0; i < 300; i++ {
		r.Tap(RPCOut, []byte(fmt.Sprintf(`{"id":%d,"method":"m","params":{"pad":"%s"}}`, i, strings.Repeat("z", 100))))
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(folder)
	if len(ents) != 1 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("the folder holds %v", names)
	}
}

// Retention removes old files to make room, but not a recording that is still
// being written, of either kind.
func TestRPCRetentionSparesWhatIsStillBeingWritten(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("hello")
	term := f.path()
	r1, err := m.StartRPC(RPCConfig{Meta: testMeta})
	if err != nil {
		t.Fatal(err)
	}
	// Both look older than the retention.
	old := time.Now().Add(-(RetainDays + 5) * 24 * time.Hour)
	for _, p := range []string{term, r1.Path()} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// Another recording starts in the same folder and runs the retention.
	r2, err := m.StartRPC(RPCConfig{Meta: testMeta, Now: func() time.Time { return time.Now().Add(time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{"terminal": term, "stream": r1.Path(), "new": r2.Path()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("the %s recording being written was removed: %v", name, err)
		}
	}
	// A terminal recording that starts runs it too, and spares the stream ones.
	f2 := newFeed(t, m)
	f2.meta.Conversation = "conv-2"
	f2.prompt("again")
	if _, err := os.Stat(r1.Path()); err != nil {
		t.Errorf("a stream recording was removed by a terminal one starting: %v", err)
	}
	// Once finished they are ordinary files again.
	_ = r1.Close()
	_ = os.Chtimes(r1.Path(), old, old) // closing wrote to it
	r3, err := m.StartRPC(RPCConfig{Meta: testMeta, Now: func() time.Time { return time.Now().Add(2 * time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Close()
	if _, err := os.Stat(r1.Path()); err == nil {
		t.Error("a finished recording past the retention was kept")
	}
}

// endless is a reader of valid lines that never ends.
type endless struct{ line []byte }

func (e *endless) Read(p []byte) (int, error) {
	n := 0
	for n+len(e.line) <= len(p) {
		n += copy(p[n:], e.line)
	}
	if n == 0 {
		n = copy(p, e.line)
	}
	return n, nil
}

func TestRPCReadersStopAtTheTotalCap(t *testing.T) {
	line, _ := json.Marshal(RPCLine{V: 1, Seq: 1, Time: "2026-10-19T12:00:00Z", Session: "s", Type: TypeRPCMessage, Dir: RPCIn, Msg: json.RawMessage(`{"pad":"` + strings.Repeat("p", 4000) + `"}`)})
	line = append(line, '\n')

	seen := 0
	st, err := ScanRPC(&endless{line}, func(RPCLine) error { seen++; return nil })
	if err != ErrRPCTooLarge || st.Damaged != 0 || seen == 0 || int64(seen)*int64(len(line)) > MaxRPCReadBytes {
		t.Errorf("ScanRPC: err %v, %d lines, damaged %d", err, seen, st.Damaged)
	}
	f, err := ReadRPC(&endless{line})
	if err != ErrRPCTooLarge || len(f.Lines) == 0 {
		t.Errorf("ReadRPC: err %v, %d lines", err, len(f.Lines))
	}
	// A stop from the caller ends the scan and is returned.
	stop := fmt.Errorf("enough")
	n := 0
	if _, err := ScanRPC(&endless{line}, func(RPCLine) error {
		if n++; n == 3 {
			return stop
		}
		return nil
	}); err != stop || n != 3 {
		t.Errorf("stop: err %v after %d lines", err, n)
	}
	// A file of ordinary size is read whole by both.
	small := strings.Repeat(string(line), 10)
	if _, err := ScanRPC(strings.NewReader(small), func(RPCLine) error { return nil }); err != nil {
		t.Errorf("small file: %v", err)
	}
	var _ io.Reader = &endless{}
}
