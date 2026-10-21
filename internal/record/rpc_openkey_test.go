package record

import (
	"os"
	"strings"
	"testing"
)

// The context of text that is still arriving keeps a BEGIN line while a private
// key has not ended. These tests are for what that line is, how long it is held,
// and the shapes of key it has to hold for.

const (
	keyLine   = "MIIEowIBAAKCAQEAzZ3k8QxY9wP2LmN5\n"
	keyBegin  = "-----BEGIN RSA PRIVATE KEY-----\n"
	keyEnd    = "-----END RSA PRIVATE KEY-----\n"
	keySecret = "ZZtopSecretBodyLine0123456789abcdef"
)

// body is about n bytes of key lines.
func body(n int) string { return strings.Repeat(keyLine, n/len(keyLine)+1) }

// recorded runs the parts through a recorder, a flush after each, and returns
// the file as it was written. A part that is exactly gapMark is a lost line.
func recorded(t *testing.T, parts ...string) string {
	t.Helper()
	r, _, _ := newRPC(t, RPCConfig{})
	for _, p := range parts {
		if p == gapMark {
			r.Gap(1)
			continue
		}
		r.Tap(RPCIn, chunk("agent_message_chunk", "s1", p))
		if err := r.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const gapMark = "\x00gap"

// After should be what follows the key and is longer than the end held back, so
// that the secret sits in the part that is written when it arrives.
var after = strings.Repeat("after the key. ", 40)

func TestRPCOpenKeyIsRedactedWhateverShapeItTakes(t *testing.T) {
	long := "-----BEGIN " + strings.Repeat("A", 200) + " PRIVATE KEY-----\n"
	cases := []struct {
		name  string
		parts []string
	}{
		{"a plain key over the context", []string{"x\n" + keyBegin + body(rpcContextBytes+2000), body(400) + keySecret + "\n" + keyEnd + after}},
		{"a fake END inside the key", []string{keyBegin + body(rpcContextBytes+2000) + "-----END CERTIFICATE-----\n" + body(300), body(400) + keySecret + "\n" + keyEnd + after}},
		{"a fake END as the last thing written", []string{keyBegin + body(rpcContextBytes+2000) + "-----END CERTIFICATE-----\n", body(400) + keySecret + "\n" + keyEnd + after}},
		{"a BEGIN line of 200 characters", []string{long + body(rpcContextBytes+2000), body(400) + keySecret + "\n" + keyEnd + after}},
		{"a second BEGIN inside the key", []string{keyBegin + body(rpcContextBytes+2000) + keyBegin + body(100), body(400) + keySecret + "\n" + keyEnd + after}},
		{"a closed key and then an open one", []string{keyBegin + body(200) + keyEnd + "between\n" + keyBegin + body(rpcContextBytes+2000), body(400) + keySecret + "\n" + keyEnd + after}},
		{"a BEGIN part way along a line of 20 KB", []string{strings.Repeat("y", 5000) + keyBegin + body(15000), body(400) + keySecret + "\n" + keyEnd + after}},
		{"a gap between the start and the rest", []string{"x\n" + keyBegin + body(rpcContextBytes+2000), gapMark, body(400) + keySecret + "\n" + keyEnd + after}},
		{"a gap, a fake END and a long BEGIN", []string{long + body(rpcContextBytes+2000) + "-----END CERTIFICATE-----\n", gapMark, body(400) + keySecret + "\n" + keyEnd + after}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			whole := strings.Join(c.parts, "")
			if !strings.Contains(whole, keySecret) || strings.Contains(Redact(whole), keySecret) {
				t.Fatalf("Redact does not hide the secret in the unsplit text, so this case proves nothing")
			}
			got := recorded(t, c.parts...)
			if strings.Contains(got, keySecret) {
				t.Errorf("the rest of the key reached the file")
			}
			if !strings.Contains(got, "after the key. ") {
				t.Errorf("the text after the key was lost")
			}
		})
	}
}

// The line kept is the same short line whatever the key's own was, and what is
// kept stays bounded however long the key goes on.
func TestContextOfKeepsAFixedBeginLineOfAnOpenKeyOnly(t *testing.T) {
	bound := rpcContextBytes + len(rpcBeginLine) + 1
	long := "-----BEGIN " + strings.Repeat("A", 200) + " PRIVATE KEY-----\n"
	for name, s := range map[string]string{
		"plain":          keyBegin + body(rpcContextBytes+500),
		"a fake END":     keyBegin + body(rpcContextBytes+500) + "-----END CERTIFICATE-----\n",
		"a long BEGIN":   long + body(rpcContextBytes+500),
		"a second BEGIN": keyBegin + body(rpcContextBytes+500) + keyBegin + body(50),
		"after a closed": keyBegin + body(100) + keyEnd + keyBegin + body(rpcContextBytes+500),
	} {
		ctx, open := contextOf("", 0, s)
		if !strings.HasPrefix(ctx, rpcBeginLine+"\n") || len(ctx) > bound || open == 0 {
			t.Errorf("%s: the context starts %q, is %d bytes (bound %d), open %d", name, ctx[:40], len(ctx), bound, open)
		}
		if strings.Count(ctx, "-----BEGIN ") != 1 && name != "a second BEGIN" {
			t.Errorf("%s: the context has the BEGIN line %d times", name, strings.Count(ctx, "-----BEGIN "))
		}
	}

	plain := strings.Repeat("z", rpcContextBytes+500)
	for name, s := range map[string]string{
		"short":                   "short",
		"no key":                  plain,
		"a closed key":            keyBegin + body(rpcContextBytes+500) + keyEnd + plain,
		"a key closed at the end": keyBegin + body(rpcContextBytes+500) + keyEnd,
		"a fake END and then the real one at the end": keyBegin + body(rpcContextBytes+500) + "-----END CERTIFICATE-----\n" + body(100) + keyEnd,
		"a key begun in the tail":                     plain + keyBegin + body(500),
		"a BEGIN that is no label":                    "-----BEGIN CERTIFICATE-----\n" + body(rpcContextBytes+500),
	} {
		ctx, open := contextOf("", 0, s)
		if ctx != tailBytes(s, rpcContextBytes) {
			t.Errorf("%s: the context was changed", name)
		}
		if name == "a key begun in the tail" && open == 0 {
			t.Errorf("%s: the key was not counted", name)
		}
		if name != "a key begun in the tail" && open != 0 {
			t.Errorf("%s: counted as open (%d)", name, open)
		}
	}
}

// A BEGIN line that is not a key, in prose, holds the text after it only up to
// rpcMaxOpenKey, however it arrives.
func TestAnUnendedBeginLineHoldsForABoundedLength(t *testing.T) {
	prev, open := "", 0
	step := 4 << 10
	for written := 0; written < rpcMaxOpenKey*2; written += step {
		piece := body(step)
		if written == 0 {
			piece = "the regexp is " + rpcBeginLine + " and so on\n" + piece
		}
		var ctx string
		ctx, open = contextOf(prev, open, piece)
		if len(ctx) > rpcContextBytes+len(rpcBeginLine)+1 {
			t.Fatalf("%d bytes written: the context is %d bytes", written, len(ctx))
		}
		if written+step > rpcMaxOpenKey+step && strings.Contains(ctx, rpcBeginLine) {
			t.Fatalf("%d bytes written: the BEGIN line is still held", written)
		}
		if written+step < rpcMaxOpenKey/2 && open == 0 {
			t.Fatalf("%d bytes written: the key is not held", written)
		}
		prev = ctx
	}
	if open != 0 {
		t.Errorf("the key is still open after %d bytes", rpcMaxOpenKey*2)
	}
}

func TestPlainTextAfterAnUnendedBeginLineIsWrittenAgainAfterTheBound(t *testing.T) {
	text := "the regexp is " + rpcBeginLine + " and so on\n"
	var parts []string
	parts = append(parts, text+body(8<<10))
	for i := 0; i < 20; i++ {
		parts = append(parts, strings.Repeat("plain words here. ", 450))
	}
	parts = append(parts, "LASTWORDSOFTHESTREAM and nothing more")
	got := recorded(t, parts...)
	if !strings.Contains(got, "LASTWORDSOFTHESTREAM") {
		t.Errorf("text %d bytes after an unended BEGIN line was still redacted", len(strings.Join(parts, "")))
	}
	if !strings.Contains(got, Redacted) {
		t.Errorf("the text right after the BEGIN line was not redacted")
	}
}

// The count of an open key is not carried over to a different key.
func TestContextOfCountsEachKeyFromItsOwnBegin(t *testing.T) {
	_, open := contextOf("", 0, keyBegin+body(rpcContextBytes+500))
	if open == 0 {
		t.Fatal("the first key is not open")
	}
	ctx, open := contextOf("x", open, keyEnd+"between\n"+keyBegin+body(100))
	if open == 0 || open > 2*len(keyBegin)+len(body(100)) {
		t.Errorf("the second key is counted as %d bytes", open)
	}
	_ = ctx
}

// Lines lost in the middle of the text after a BEGIN line do not start the
// bound again: the count of the open key is carried across each gap.
func TestTheBoundOfAnOpenKeyRunsOnAcrossGaps(t *testing.T) {
	parts := []string{"the regexp is " + rpcBeginLine + " and so on\n" + body(8<<10)}
	for i := 0; i < 20; i++ {
		parts = append(parts, strings.Repeat("plain words here. ", 450), gapMark)
	}
	parts = append(parts, "LASTWORDSOFTHESTREAM and nothing more")
	got := recorded(t, parts...)
	if !strings.Contains(got, "LASTWORDSOFTHESTREAM") {
		t.Errorf("text 160 KB after an unended BEGIN line, with gaps, was still redacted")
	}
}
