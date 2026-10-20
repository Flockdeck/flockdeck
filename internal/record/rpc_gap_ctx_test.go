package record

import (
	"os"
	"strings"
	"testing"
)

// A label written before a gap still finds its value after it: the text written
// before the gap stays as the context the text after it is redacted with.
func TestRPCGapKeepsTheContextForRedaction(t *testing.T) {
	const secret = "Tr0ub4dor&3xyz"
	cases := map[string][]string{
		"label then value":           {"the password:", " " + secret + " tail"},
		"label and space then value": {"the password: ", secret + " tail"},
	}
	for name, parts := range cases {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(Redact(strings.Join(parts, "")), secret) {
				t.Fatal("Redact does not find the secret in the unsplit text, so this case proves nothing")
			}
			r, _, _ := newRPC(t, RPCConfig{})
			r.Tap(RPCIn, chunk("agent_message_chunk", "s1", parts[0]))
			r.Gap(2)
			r.Tap(RPCIn, chunk("agent_message_chunk", "s1", parts[1]))
			f := read(t, r)
			b, err := os.ReadFile(r.Path())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), secret) {
				t.Errorf("the secret reached the file:\n%s", b)
			}
			if got := lineTypes(f); got != "rpc_started rpc_message rpc_gap rpc_message rpc_stopped" {
				t.Errorf("lines %q", got)
			}
			texts, _ := chunkTexts(f)
			if got := texts["agent_message_chunk/s1"]; !strings.Contains(got, Redacted) {
				t.Errorf("nothing was redacted: %q", got)
			}
		})
	}
}

// The text on both sides of a gap is all written, and every message is counted
// once, whatever the size of the pieces it came in.
func TestRPCGapCountsEveryMessageOnce(t *testing.T) {
	whole := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 120)
	for _, size := range []int{1, 7, 61} {
		r, _, _ := newRPC(t, RPCConfig{})
		var want strings.Builder
		const taps = 78
		for i := 0; i < taps; i++ {
			piece := whole[i*size : (i+1)*size]
			want.WriteString(piece)
			r.Tap(RPCIn, chunk("agent_message_chunk", "s1", piece))
			if i == 4 {
				r.Gap(3)
			}
		}
		f := read(t, r)
		texts, merged := chunkTexts(f)
		if got := texts["agent_message_chunk/s1"]; got != want.String() {
			t.Errorf("size %d: text %q, want %q", size, got, want.String())
		}
		if got := merged["agent_message_chunk/s1"]; got != taps {
			t.Errorf("size %d: the lines stand for %d messages, want %d", size, got, taps)
		}
	}
}
