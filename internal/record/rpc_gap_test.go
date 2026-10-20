package record

import (
	"os"
	"strings"
	"testing"
)

func lineTypes(f RPCFile) string {
	var types []string
	for _, l := range f.Lines {
		types = append(types, l.Type)
	}
	return strings.Join(types, " ")
}

func TestRPCGapWritesOneLineAtTheRightPlace(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":1,"method":"a"}`))
	r.Gap(3)
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":2,"method":"b"}`))
	f := read(t, r)

	if got, want := lineTypes(f), "rpc_started rpc_message rpc_gap rpc_message rpc_stopped"; got != want {
		t.Errorf("lines %q, want %q", got, want)
	}
	gap := f.Lines[2]
	if gap.Dropped != 3 || gap.Text == "" || gap.Time == "" || gap.Seq != 3 || gap.Dir != "" || gap.Msg != nil {
		t.Errorf("gap line %+v", gap)
	}
}

// Pending coalesced text is written before the gap, so the gap sits where the
// lines were lost and not before the text that came first.
func TestRPCGapComesAfterTextHeldBeforeIt(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "hello"))
	r.Gap(1)
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "again"))
	f := read(t, r)
	if got, want := lineTypes(f), "rpc_started rpc_message rpc_gap rpc_message rpc_stopped"; got != want {
		t.Errorf("lines %q, want %q", got, want)
	}
}

func TestRPCGapLineMatchesTheSchemaAndIsReadBack(t *testing.T) {
	schema := loadRPCSchema(t)
	r, _, _ := newRPC(t, RPCConfig{})
	r.Gap(7)
	f := read(t, r)
	raw, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if errs := schema.Validate([]byte(line)); len(errs) != 0 {
			t.Errorf("line does not match the schema: %v\n%s", errs, line)
		}
		found = found || strings.Contains(line, `"rpc_gap"`)
	}
	if !found {
		t.Fatal("no rpc_gap line was written")
	}
	// The reader hands it over like any line, and counts nothing as damaged.
	if f.Damaged != 0 {
		t.Errorf("damaged = %d", f.Damaged)
	}
	var got []RPCLine
	for _, l := range f.Lines {
		if l.Type == TypeRPCGap {
			got = append(got, l)
		}
	}
	if len(got) != 1 || got[0].Dropped != 7 {
		t.Errorf("read back %+v", got)
	}
}

func TestRPCGapSchemaRefusesAMalformedGap(t *testing.T) {
	schema := loadRPCSchema(t)
	const head = `{"v":1,"seq":2,"time":"2026-10-19T12:00:00Z","session":"20261019T120000Z-abc.rpc","type":"rpc_gap"`
	for name, line := range map[string]string{
		"no count":   head + `,"text":"x"}`,
		"no text":    head + `,"dropped":2}`,
		"zero count": head + `,"dropped":0,"text":"x"}`,
	} {
		if errs := schema.Validate([]byte(line)); len(errs) == 0 {
			t.Errorf("%s: a malformed rpc_gap was accepted", name)
		}
	}
}

func TestRPCGapOfZeroOrLessWritesNothing(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Gap(0)
	r.Gap(-5)
	f := read(t, r)
	if got, want := lineTypes(f), "rpc_started rpc_stopped"; got != want {
		t.Errorf("lines %q, want %q", got, want)
	}
}

func TestRPCGapAfterCloseIsSafe(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{})
	r.Tap(RPCOut, []byte(`{"jsonrpc":"2.0","id":1,"method":"a"}`))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	seq := r.seq
	r.mu.Unlock()
	r.Gap(4)
	// Nothing was queued either: a line buffered after the close is never
	// written, but it would count and could be flushed by a later call.
	r.mu.Lock()
	queued := r.seq != seq || r.w.Buffered() != 0
	r.mu.Unlock()
	if queued {
		t.Error("a gap after Close was queued")
	}
	after, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a gap after Close changed the file")
	}
}
