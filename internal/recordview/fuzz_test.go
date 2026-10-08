package recordview

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/record"
)

// checkEntry holds for every entry parseLine returns, whatever the input was.
func checkEntry(t *testing.T, e Entry) {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("entry does not encode: %v", err)
	}
	if len(b) > MaxEntryBytes+2<<10 {
		// parseLine itself does not drop; pageOf does. Bound it loosely here.
		t.Fatalf("entry of %d bytes", len(b))
	}
	var back any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("entry is not JSON: %v", err)
	}
	walk(t, back)
	if !knownTypes[e.Type] || e.Seq < 0 {
		t.Fatalf("bad envelope: %+v", e)
	}
}

func walk(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case string:
		// Redacting what was already redacted finds nothing more, apart from a cut
		// made by Clip, whose marker is not text the rules were written for.
		if !strings.Contains(x, "…[clipped") && record.Redact(x) != x {
			t.Fatalf("output is not fully redacted: %q", x)
		}
		if !utf8.ValidString(x) {
			t.Fatalf("invalid UTF-8 in output: %q", x)
		}
		for _, r := range x {
			if unsafeRune(r) {
				t.Fatalf("unsafe rune %U in output %q", r, x)
			}
			if unicode.Is(unicode.Cs, r) {
				t.Fatalf("surrogate in output")
			}
		}
	case []any:
		for _, e := range x {
			walk(t, e)
		}
	case map[string]any:
		for k, e := range x {
			walk(t, k)
			walk(t, e)
		}
	}
}

func FuzzParseLine(f *testing.F) {
	f.Add([]byte(started()))
	f.Add([]byte(line(2, "user_prompt", map[string]any{"text": "key " + ghToken + " end"})))
	f.Add([]byte(line(3, "tool_call", map[string]any{"tool": "Bash", "input": map[string]any{"command": "x " + awsKey, "api_key": "k", "list": []any{1, "a", nil}}})))
	f.Add([]byte(line(4, "tool_result", map[string]any{"output": "p\x1b[0m‮", "isError": true, "interrupted": false})))
	f.Add([]byte(`{"v":2,"seq":1,"time":"2026-10-01T10:15:30Z","type":"tool_call","input":[[[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]]}`))
	f.Add([]byte(`{"v":2,"seq":1,"time":"2026-10-01T10:15:30Z","type":"user_prompt","text":"\ud800 ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	f.Add([]byte(`{"v":1}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		e, ok := parseLine(b)
		if !ok {
			return
		}
		checkEntry(t, e)
	})
}

// FuzzPage feeds whole files and cursors to the pager. Whatever the bytes are,
// it must not panic, must stay inside its limits, and must make progress.
func FuzzPage(f *testing.F) {
	f.Add([]byte(started()+"\n"+line(2, "user_prompt", map[string]any{"text": "a"})+"\n"), 0, 5)
	f.Add([]byte(started()+"\n{"), 0, 1)
	f.Add([]byte("\n\n\n"), 1, 3)
	f.Add([]byte(started()+"\n"+line(2, "user_prompt", map[string]any{"text": "a"})+"\n"), 3, 0)
	f.Fuzz(func(t *testing.T, data []byte, cursor, max int) {
		rd := bytes.NewReader(data)
		p, err := pageOf(rd, int64(len(data)), cursor, max)
		if err != nil {
			return
		}
		if len(p.Entries) > MaxPageEntries {
			t.Fatalf("%d entries", len(p.Entries))
		}
		if p.Next < cursor || p.Next > len(data) {
			t.Fatalf("next %d outside [%d, %d]", p.Next, cursor, len(data))
		}
		if p.Next > 0 && data[p.Next-1] != '\n' {
			t.Fatalf("next %d is not after a line break", p.Next)
		}
		if p.Next == cursor && len(p.Entries) > 0 {
			t.Fatal("entries without progress")
		}
		for _, e := range p.Entries {
			checkEntry(t, e)
		}
		// A returned cursor is always accepted.
		if _, err := pageOf(rd, int64(len(data)), p.Next, max); err != nil {
			t.Fatalf("own cursor refused: %v", err)
		}
	})
}
