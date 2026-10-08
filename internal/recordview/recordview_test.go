package recordview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Built from pieces so that no source line looks like a live credential.
var (
	ghToken = "ghp_" + strings.Repeat("a1B2c3", 6)
	awsKey  = "AKIA" + "IOSFODNN7EXAMPLE"
)

// line is a transcript line with the envelope filled in; fields override it.
func line(seq int, typ string, fields map[string]any) string {
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

func started() string {
	return line(1, "recording_started", map[string]any{"text": "start of the transcript"})
}

func writeFile(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func pageAll(t *testing.T, r *Reader, max int) []Entry {
	t.Helper()
	var out []Entry
	cursor := 0
	for i := 0; i < 100000; i++ {
		p, err := r.Page(cursor, max)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Entries...)
		if p.Done {
			return out
		}
		if p.Next <= cursor && len(p.Entries) == 0 {
			t.Fatalf("no progress at cursor %d", cursor)
		}
		cursor = p.Next
	}
	t.Fatal("did not finish")
	return nil
}

func TestPagingVisitsEveryEntryOnce(t *testing.T) {
	lines := []string{started()}
	for i := 2; i <= 30; i++ {
		lines = append(lines, line(i, "user_prompt", map[string]any{"text": fmt.Sprintf("prompt %d", i)}))
	}
	r, err := Open(writeFile(t, lines...))
	if err != nil {
		t.Fatal(err)
	}
	for _, max := range []int{1, 4, 7, 29, 30, 1000, 0, -3} {
		got := pageAll(t, r, max)
		if len(got) != 30 {
			t.Fatalf("max %d: %d entries, want 30", max, len(got))
		}
		for i, e := range got {
			if e.Seq != int64(i+1) {
				t.Fatalf("max %d: entry %d has seq %d", max, i, e.Seq)
			}
		}
	}
	p, _ := r.Page(0, 5)
	if len(p.Entries) != 5 || p.Done {
		t.Fatalf("first page: %d entries, done=%v", len(p.Entries), p.Done)
	}
	p, _ = r.Page(0, 100000)
	if len(p.Entries) != 30 || !p.Done {
		t.Fatalf("a huge max must be lowered to the cap, got %d entries done=%v", len(p.Entries), p.Done)
	}
	if h := r.Header(); h.Project != "shop" || h.Agent != "claude" || h.Conversation != "0123abcd-5e6f" || h.Started == "" {
		t.Fatalf("header: %+v", h)
	}
}

func TestPageBytesBudget(t *testing.T) {
	lines := []string{started()}
	big := strings.Repeat("x", 30<<10)
	for i := 2; i <= 60; i++ {
		lines = append(lines, line(i, "assistant_message", map[string]any{"text": big}))
	}
	r, err := Open(writeFile(t, lines...))
	if err != nil {
		t.Fatal(err)
	}
	cursor, entries := 0, 0
	for {
		p, err := r.Page(cursor, MaxPageEntries)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(p)
		if len(b) > MaxPageBytes+MaxEntryBytes+1024 {
			t.Fatalf("page of %d bytes", len(b))
		}
		if len(p.Entries) == 0 && !p.Done {
			t.Fatal("empty page that is not done")
		}
		entries += len(p.Entries)
		if p.Done {
			break
		}
		cursor = p.Next
	}
	if entries != 60 {
		t.Fatalf("%d entries, want 60", entries)
	}
}

func TestOnlyVersion2OfAKnownTranscriptIsOpened(t *testing.T) {
	v1 := `{"v":1,"seq":1,"time":"2026-10-01T10:15:30Z","session":"s","pane":"p1","paneName":"x","type":"recording_started","text":"start"}`
	notStart := line(1, "user_prompt", map[string]any{"text": "hi"})
	for name, content := range map[string]string{
		"v1":              v1 + "\n",
		"not a start":     notStart + "\n",
		"not json":        "hello\n",
		"empty":           "",
		"only a newline":  "\n",
		"no line break":   started(),
		"v is a string":   strings.Replace(started(), `"v":2`, `"v":"2"`, 1) + "\n",
		"v is 3":          strings.Replace(started(), `"v":2`, `"v":3`, 1) + "\n",
		"v missing":       strings.Replace(started(), `,"v":2`, ``, 1) + "\n",
		"bad time":        strings.Replace(started(), `10:15:01.5Z`, `yesterday`, 1) + "\n",
		"binary":          "\x00\x01\x02\xff\n",
		"start then junk": started() + "\n",
	} {
		p := filepath.Join(t.TempDir(), "f.jsonl")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Open(p)
		if name == "start then junk" {
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			continue
		}
		if name == "bad time" {
			// The first line's time is not needed to open; the line itself is dropped on a page.
			if err != nil {
				continue
			}
			if got := pageAll(t, r, 10); len(got) != 0 {
				t.Fatalf("a line with a bad time was shown: %+v", got)
			}
			continue
		}
		if !errors.Is(err, ErrUnsupported) || r != nil {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestOpenRefusals(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(filepath.Join(dir, "nope.jsonl")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrUnsupported) {
		t.Errorf("directory: %v", err)
	}
	if _, err := Open(""); err == nil {
		t.Error("empty path opened")
	}
	// An error never carries the path or OS text.
	_, err := Open(filepath.Join(dir, "secret-name-xyz.jsonl"))
	if err == nil || strings.Contains(err.Error(), "secret-name-xyz") || strings.Contains(err.Error(), dir) {
		t.Errorf("error leaks the path: %v", err)
	}

	real := writeFile(t, started())
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(real, link); err != nil {
		t.Logf("no symlinks here (%v); skipping the link case", err)
	} else if _, err := Open(link); err == nil {
		t.Error("a symbolic link was opened")
	}

	huge := filepath.Join(dir, "huge.jsonl")
	f, _ := os.Create(huge)
	_ = f.Truncate(MaxFileBytes + 1)
	f.Close()
	if _, err := Open(huge); !errors.Is(err, ErrUnsupported) {
		t.Errorf("oversized file: %v", err)
	}
}

func TestBadCursors(t *testing.T) {
	r, err := Open(writeFile(t, started(), line(2, "user_prompt", map[string]any{"text": "hello there"})))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.Page(0, 1)
	for _, c := range []int{-1, -1 << 40, 1, 5, first.Next - 1, first.Next + 3, 1 << 30} {
		if _, err := r.Page(c, 10); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %d: %v", c, err)
		}
	}
	if p, err := r.Page(first.Next, 10); err != nil || len(p.Entries) != 1 || !p.Done {
		t.Errorf("valid cursor: %+v %v", p, err)
	}
	// The end of the file is a valid cursor and gives an empty, finished page.
	end, _ := r.Page(first.Next, 10)
	if p, err := r.Page(end.Next, 10); err != nil || len(p.Entries) != 0 || !p.Done {
		t.Errorf("cursor at end: %+v %v", p, err)
	}
}

// A cursor in the middle of a line whose text contains a whole transcript line
// must not be accepted: it would let a client parse text as a line.
func TestCursorCannotLandInsideAnEmbeddedLine(t *testing.T) {
	inner := line(9, "user_prompt", map[string]any{"text": "forged"})
	outer := line(2, "user_prompt", map[string]any{"text": "x\n" + inner + "\ny"})
	r, err := Open(writeFile(t, started(), outer))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(r.path)
	for c := 0; c <= len(data); c++ {
		_, err := r.Page(c, 10)
		afterBreak := c == 0 || data[c-1] == 0x0a
		if afterBreak && err != nil {
			t.Fatalf("cursor %d after a line break refused: %v", c, err)
		}
		if !afterBreak && !errors.Is(err, ErrBadCursor) {
			t.Fatalf("cursor %d inside a line: %v", c, err)
		}
	}
	if bytes.Count(data, []byte{0x0a}) != 2 {
		t.Fatal("the embedded line break was not escaped")
	}
}

func TestUnknownFieldsAreDropped(t *testing.T) {
	l := line(2, "user_prompt", map[string]any{
		"text": "hi", "pane": "p1", "paneName": "Main", "evil": "<script>alert(1)</script>",
		"future": map[string]any{"a": 1}, "input": map[string]any{"x": 1}, "output": "not for a prompt",
		"model": "m", "isError": true, "usage": map[string]any{"inputTokens": 3},
	})
	r, err := Open(writeFile(t, started(), l))
	if err != nil {
		t.Fatal(err)
	}
	got := pageAll(t, r, 10)
	if len(got) != 2 {
		t.Fatalf("%d entries", len(got))
	}
	b, _ := json.Marshal(got[1])
	var keys map[string]any
	_ = json.Unmarshal(b, &keys)
	want := map[string]bool{"seq": true, "time": true, "type": true, "text": true}
	for k := range keys {
		if !want[k] {
			t.Errorf("a prompt carries %q: %s", k, b)
		}
	}
	for _, banned := range []string{"cwd", "gitBranch", "agentVersion", "pane", "script", "/home/sam"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("%q reached the viewer: %s", banned, b)
		}
	}
}

func TestEveryTypeKeepsOnlyItsFields(t *testing.T) {
	all := map[string]any{
		"text": "t", "model": "m", "usage": map[string]any{"inputTokens": 1}, "stopReason": "end_turn", "title": "ti",
		"trigger": "auto", "tokensBefore": 10, "tokensAfter": 5, "tool": "Bash", "toolUseId": "u1",
		"input": map[string]any{"command": "ls"}, "output": "o", "isError": false, "interrupted": false,
	}
	allowed := map[string][]string{
		"recording_started":      {"text"},
		"recording_stopped":      {"text"},
		"recording_truncated":    {"text"},
		"user_prompt":            {"text"},
		"assistant_message":      {"text", "model", "usage", "stopReason"},
		"tool_call":              {"tool", "toolUseId", "input", "model", "usage", "stopReason"},
		"tool_result":            {"tool", "toolUseId", "output", "isError", "interrupted"},
		"conversation_title":     {"title"},
		"conversation_compacted": {"trigger", "tokensBefore", "tokensAfter"},
	}
	for typ, fields := range allowed {
		e, ok := parseLine([]byte(line(2, typ, all)))
		if !ok {
			t.Fatalf("%s not parsed", typ)
		}
		b, _ := json.Marshal(e)
		var got map[string]any
		_ = json.Unmarshal(b, &got)
		delete(got, "seq")
		delete(got, "time")
		delete(got, "type")
		want := map[string]bool{}
		for _, f := range fields {
			want[f] = true
		}
		for k := range got {
			if !want[k] {
				t.Errorf("%s carries %q", typ, k)
			}
		}
		for f := range want {
			if _, ok := got[f]; !ok {
				t.Errorf("%s lost %q", typ, f)
			}
		}
	}
	// Unknown types and other versions are skipped, mid-file too.
	r, err := Open(writeFile(t, started(),
		line(2, "mystery", map[string]any{"text": "x"}),
		strings.Replace(line(3, "user_prompt", map[string]any{"text": "old"}), `"v":2`, `"v":1`, 1),
		"{not json",
		"",
		line(4, "user_prompt", map[string]any{"text": "kept"})))
	if err != nil {
		t.Fatal(err)
	}
	got := pageAll(t, r, 10)
	if len(got) != 2 || got[1].Text != "kept" {
		t.Fatalf("got %+v", got)
	}
	p, _ := r.Page(0, 10)
	if p.Skipped != 3 {
		t.Errorf("skipped = %d, want 3 (the blank line is not counted)", p.Skipped)
	}
}

func TestRedactedAgainOnTheWayOut(t *testing.T) {
	// The file claims nothing was redacted. It is redacted anyway.
	secretLines := []string{
		line(2, "user_prompt", map[string]any{"text": "use " + ghToken + " and password=hunter2 please"}),
		line(3, "assistant_message", map[string]any{"text": "key " + awsKey}),
		line(4, "tool_call", map[string]any{"tool": "Bash", "toolUseId": "u1", "input": map[string]any{
			"command": "curl -H 'Authorization: Bearer abcdefghijklmnop' https://x", "api_key": "plainvalue", "nested": []any{map[string]any{"token": "t0k3n"}, ghToken},
		}}),
		line(5, "tool_result", map[string]any{"tool": "Bash", "toolUseId": "u1", "output": "DB_PASSWORD=swordfish\nok", "isError": false, "interrupted": false}),
		line(6, "conversation_title", map[string]any{"title": "rotate " + ghToken}),
		line(7, "recording_truncated", map[string]any{"text": "oops " + ghToken}),
	}
	r, err := Open(writeFile(t, append([]string{started()}, secretLines...)...))
	if err != nil {
		t.Fatal(err)
	}
	got := pageAll(t, r, 50)
	b, _ := json.Marshal(got)
	for _, leak := range []string{ghToken, "hunter2", awsKey, "abcdefghijklmnop", "plainvalue", "t0k3n", "swordfish"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%q reached the viewer: %s", leak, b)
		}
	}
	for _, e := range got[1:] {
		if !e.Redacted {
			t.Errorf("seq %d (%s) changed but is not marked redacted", e.Seq, e.Type)
		}
	}
	if got[0].Redacted {
		t.Error("a clean line is marked redacted")
	}
	// A control character inside a token must not hide it from redaction.
	broken := ghToken[:10] + "\x1b" + "‮" + ghToken[10:]
	e, ok := parseLine([]byte(line(2, "user_prompt", map[string]any{"text": "x " + broken})))
	if !ok || strings.Contains(e.Text, ghToken[:12]) || strings.ContainsAny(e.Text, "\x1b‮") {
		t.Errorf("token split by control characters: %q", e.Text)
	}
}

func TestClipping(t *testing.T) {
	long := strings.Repeat("é", 30<<10) // 60 KiB
	e, ok := parseLine([]byte(line(2, "user_prompt", map[string]any{"text": long})))
	if !ok {
		t.Fatal("not parsed")
	}
	if len(e.Text) > record.MaxMessageBytes+64 || !strings.Contains(e.Text, "…[clipped ") || !utf8.ValidString(e.Text) {
		t.Fatalf("text of %d bytes, valid=%v", len(e.Text), utf8.ValidString(e.Text))
	}
	if e.Clipped["text"] != len(long) {
		t.Errorf("clipped[text] = %d, want %d", e.Clipped["text"], len(long))
	}
	e, _ = parseLine([]byte(line(3, "tool_result", map[string]any{"output": strings.Repeat("o", 20<<10), "isError": false, "interrupted": false})))
	if len(e.Output) > record.MaxFieldBytes+64 || e.Clipped["output"] != 20<<10 {
		t.Errorf("output of %d bytes, clipped %v", len(e.Output), e.Clipped)
	}
	// A line the writer already clipped keeps its original length if it is larger.
	e, _ = parseLine([]byte(line(4, "tool_result", map[string]any{"output": "abc…[clipped 99 bytes]", "clipped": map[string]any{"output": 5000, "cwd": 7, "bogus": 1}, "isError": false, "interrupted": false})))
	if !reflect.DeepEqual(e.Clipped, map[string]int{"output": 5000}) {
		t.Errorf("clipped = %v", e.Clipped)
	}
	// Strings inside input are clipped at the field limit.
	e, _ = parseLine([]byte(line(5, "tool_call", map[string]any{"tool": "Write", "input": map[string]any{"content": strings.Repeat("c", 9000)}})))
	in := e.Input.(map[string]any)["content"].(string)
	if len(in) > record.MaxFieldBytes+64 || e.Clipped["input"] == 0 {
		t.Errorf("input string of %d bytes, clipped %v", len(in), e.Clipped)
	}
}

func TestHostileInputShapes(t *testing.T) {
	deep := strings.Repeat("[", 3000) + strings.Repeat("]", 3000)
	wide := "[" + strings.TrimSuffix(strings.Repeat("0,", 5000), ",") + "]"
	manyStrings := "[" + strings.TrimSuffix(strings.Repeat(`"`+strings.Repeat("s", 7000)+`",`, 40), ",") + "]"
	for name, input := range map[string]string{"deep": deep, "wide": wide, "many strings": manyStrings} {
		raw := fmt.Sprintf(`{"v":2,"seq":2,"time":"2026-10-01T10:15:30Z","type":"tool_call","tool":"X","input":%s}`, input)
		e, ok := parseLine([]byte(raw))
		if !ok {
			t.Errorf("%s: line dropped", name)
			continue
		}
		if e.Input != omittedInput {
			t.Errorf("%s: input not replaced: %T", name, e.Input)
		}
		if e.Clipped["input"] == 0 {
			t.Errorf("%s: no clipped note", name)
		}
	}
	// Deeper than the JSON decoder allows at all.
	raw := `{"v":2,"seq":2,"time":"2026-10-01T10:15:30Z","type":"tool_call","tool":"X","input":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}`
	if _, ok := parseLine([]byte(raw)); ok {
		t.Error("a 20000-deep input was accepted")
	}
	// Numbers survive exactly.
	e, _ := parseLine([]byte(`{"v":2,"seq":2,"time":"2026-10-01T10:15:30Z","type":"tool_call","tool":"X","input":{"n":12345678901234567890}}`))
	if b, _ := json.Marshal(e.Input); string(b) != `{"n":12345678901234567890}` {
		t.Errorf("number changed: %s", b)
	}
}

func TestLongLinesAreSkippedAndReadingGoesOn(t *testing.T) {
	huge := line(2, "user_prompt", map[string]any{"text": "x", "future": strings.Repeat("z", 3<<20)})
	r, err := Open(writeFile(t, started(), huge, line(3, "user_prompt", map[string]any{"text": "after"})))
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Page(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Entries) != 2 || p.Entries[1].Text != "after" || p.Skipped != 1 || !p.Done {
		t.Fatalf("%+v", p)
	}
}

func TestLastLineWithoutLineBreakWaits(t *testing.T) {
	p := writeFile(t, started())
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	tail := line(2, "user_prompt", map[string]any{"text": "half"})
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(tail[:len(tail)/2])
	pg, _ := r.Page(0, 10)
	if len(pg.Entries) != 1 || !pg.Done {
		t.Fatalf("half a line: %+v", pg)
	}
	// Even a complete line is not read before its line break: it is the writer's mark that the line is whole.
	_, _ = f.WriteString(tail[len(tail)/2:])
	pg2, _ := r.Page(0, 10)
	if len(pg2.Entries) != 1 || pg2.Next != pg.Next {
		t.Fatalf("unterminated line was consumed: %+v", pg2)
	}
	_, _ = f.WriteString("\n")
	f.Close()
	pg3, err := r.Page(pg.Next, 10)
	if err != nil || len(pg3.Entries) != 1 || pg3.Entries[0].Text != "half" {
		t.Fatalf("after the line break: %+v %v", pg3, err)
	}
}

func TestOneCallDoesBoundedWork(t *testing.T) {
	// 6 MiB of blank-ish junk lines then a real one: the first call stops after
	// MaxScanBytes with Done false, and following the cursors finds the line.
	junk := strings.Repeat("{}\n", 2<<20)
	p := filepath.Join(t.TempDir(), "a.jsonl")
	if err := os.WriteFile(p, []byte(started()+"\n"+junk+line(2, "user_prompt", map[string]any{"text": "end"})+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := r.Page(0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Done {
		t.Fatalf("one call read the whole 6 MiB file")
	}
	got := pageAll(t, r, 200)
	if len(got) != 2 || got[1].Text != "end" {
		t.Fatalf("got %d entries", len(got))
	}
}

func TestUnsafeCharactersAreRemoved(t *testing.T) {
	e, _ := parseLine([]byte(line(2, "user_prompt", map[string]any{
		"text": "a\x1b[31mred\x07\x00b‮evil⁦x‏y z\r\n\tq\u0085end",
	})))
	if e.Text != "a[31mredbevilxy\nz\n\tqend" {
		t.Errorf("text = %q", e.Text)
	}
	e, _ = parseLine([]byte(line(2, "tool_call", map[string]any{"tool": "Ba\x1bsh", "input": map[string]any{"k\x1b": "v\x1b"}})))
	if e.Tool != "Bash" || !reflect.DeepEqual(e.Input, map[string]any{"k": "v"}) {
		t.Errorf("tool %q input %v", e.Tool, e.Input)
	}
	raw := []byte("{\"v\":2,\"seq\":2,\"time\":\"2026-10-01T10:15:30Z\",\"type\":\"user_prompt\",\"text\":\"bad \xff\xfe bytes\"}")
	e, ok := parseLine(raw)
	if !ok || !utf8.ValidString(e.Text) {
		t.Errorf("invalid utf-8: %q", e.Text)
	}
	long := strings.Repeat("あ", 200)
	e, _ = parseLine([]byte(line(2, "tool_call", map[string]any{"tool": long})))
	if len(e.Tool) > maxIDBytes || !utf8.ValidString(e.Tool) {
		t.Errorf("tool of %d bytes", len(e.Tool))
	}
}

func TestTimesAreNormalised(t *testing.T) {
	e, ok := parseLine([]byte(`{"v":2,"seq":1,"time":"2026-10-01T12:15:30.25+02:00","type":"user_prompt","text":"x"}`))
	if !ok || e.Time != "2026-10-01T10:15:30.25Z" {
		t.Errorf("%v %q", ok, e.Time)
	}
	for _, bad := range []string{``, `"x"`, `"2026-13-01T00:00:00Z"`, `"<img src=x>"`, `5`} {
		if _, ok := parseLine([]byte(`{"v":2,"seq":1,"time":` + bad + `,"type":"user_prompt","text":"x"}`)); ok {
			t.Errorf("time %s accepted", bad)
		}
	}
}

func TestNegativeAndOddNumbers(t *testing.T) {
	e, ok := parseLine([]byte(`{"v":2,"seq":3,"time":"2026-10-01T10:15:30Z","type":"conversation_compacted","trigger":"auto","tokensBefore":-5,"tokensAfter":22}`))
	if !ok || e.TokensBefore != nil || e.TokensAfter == nil || *e.TokensAfter != 22 {
		t.Errorf("%+v", e)
	}
	if _, ok := parseLine([]byte(`{"v":2,"seq":-1,"time":"2026-10-01T10:15:30Z","type":"user_prompt","text":"x"}`)); ok {
		t.Error("negative seq accepted")
	}
	if _, ok := parseLine([]byte(`{"v":2,"seq":1.5,"time":"2026-10-01T10:15:30Z","type":"user_prompt","text":"x"}`)); ok {
		t.Error("fractional seq accepted")
	}
	e, _ = parseLine([]byte(`{"v":2,"seq":3,"time":"2026-10-01T10:15:30Z","type":"assistant_message","text":"x","usage":{"inputTokens":-1}}`))
	if e.Usage != nil {
		t.Errorf("usage of nothing kept: %+v", e.Usage)
	}
}

func TestConcurrentPages(t *testing.T) {
	lines := []string{started()}
	for i := 2; i <= 200; i++ {
		lines = append(lines, line(i, "user_prompt", map[string]any{"text": strings.Repeat("w", 200)}))
	}
	r, err := Open(writeFile(t, lines...))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := pageAll(t, r, 13); len(got) != 200 {
				t.Errorf("%d entries", len(got))
			}
		}()
	}
	wg.Wait()
}

// The reader works on what the real writer produces, so a change to the format
// that the reader does not follow fails here.
func TestReadsWhatTheWriterWrites(t *testing.T) {
	dir := t.TempDir()
	m := record.NewManager(func() (string, error) { return dir, nil })
	meta := record.Meta{Project: "shop", ProjectRoot: "/work/shop", Agent: "claude", Conversation: "0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d"}
	at := time.Date(2026, 10, 1, 10, 15, 30, 0, time.UTC)
	one, two := 6, 212
	events := []transcript.ExportEvent{
		{Time: at, Kind: transcript.ExportPrompt, Text: "the key is " + ghToken, Cwd: "/home/sam/shop", GitBranch: "main"},
		{Time: at.Add(time.Second), Kind: transcript.ExportMessage, Text: "on it", Model: "claude-opus-5-5", StopReason: "end_turn", Usage: &transcript.ExportUsage{InputTokens: &one, OutputTokens: &two}},
		{Time: at.Add(2 * time.Second), Kind: transcript.ExportToolCall, Tool: "Bash", ToolUseID: "toolu_01", Input: map[string]any{"command": "go test ./...", "password": "pw"}},
		{Time: at.Add(3 * time.Second), Kind: transcript.ExportToolResult, Tool: "Bash", ToolUseID: "toolu_01", Output: "ok", IsError: false},
		{Time: at.Add(4 * time.Second), Kind: transcript.ExportTitle, Text: "Add a retry"},
		{Time: at.Add(5 * time.Second), Kind: transcript.ExportCompact, Trigger: "auto", TokensBefore: 970192, TokensAfter: 22085},
	}
	for _, ev := range events {
		if err := m.Write(meta, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Finish(meta); err != nil {
		t.Fatal(err)
	}
	infos, err := record.List(func() (string, error) { return dir, nil })
	if err != nil || len(infos) != 1 {
		t.Fatalf("list: %v %v", infos, err)
	}
	r, err := Open(infos[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	got := pageAll(t, r, 3)
	var types []string
	for _, e := range got {
		types = append(types, e.Type)
	}
	want := []string{"recording_started", "user_prompt", "assistant_message", "tool_call", "tool_result", "conversation_title", "conversation_compacted", "recording_stopped"}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("types = %v", types)
	}
	if got[2].Usage == nil || *got[2].Usage.OutputTokens != 212 || got[2].Model != "claude-opus-5-5" || got[2].StopReason != "end_turn" {
		t.Errorf("assistant: %+v", got[2])
	}
	if got[4].IsError == nil || *got[4].IsError || got[4].Interrupted == nil {
		t.Errorf("tool_result flags: %+v", got[4])
	}
	if got[6].TokensBefore == nil || *got[6].TokensBefore != 970192 {
		t.Errorf("compacted: %+v", got[6])
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), ghToken) || strings.Contains(string(b), "/home/sam") {
		t.Errorf("leak: %s", b)
	}
}
