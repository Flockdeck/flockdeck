package record

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A label can be in text that is already written when its value arrives, past
// what the hold-back keeps. The value is redacted with the written text in view.
func TestRPCRedactsAValueWhoseLabelWasAlreadyWritten(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("MIIEowIBAAKCAQEAzZ3k8QxY9wP2LmN5\n", 8) + "-----END RSA PRIVATE KEY-----\n"
	cases := []struct {
		name   string
		secret string
		parts  []string
	}{
		{"env name then value", "hunter2xyz9", []string{"set DB_PASSWORD=", "hunter2xyz9 tail"}},
		{"env name written, value in the held end", "hunter2xyz9", []string{"please set DB_PASSWORD= ", "hunter2xyz9 tail"}},
		{"label then value", "Tr0ub4dor&3xyz", []string{"the password:", " Tr0ub4dor&3xyz tail"}},
		{"label and space then value", "Tr0ub4dor&3xyz", []string{"the password: ", "Tr0ub4dor&3xyz tail"}},
		{"a key split at byte 60", "MIIEowIBAAKCAQEAzZ3k8QxY9wP2LmN5", []string{"here it is:\n" + pem[:60], pem[60:] + "\nthat was it"}},
		{"a key split in three", "MIIEowIBAAKCAQEAzZ3k8QxY9wP2LmN5", []string{"here it is:\n" + pem[:60], pem[60:200], pem[200:] + "\nthat was it"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			whole := strings.Join(c.parts, "")
			if strings.Contains(Redact(whole), c.secret) {
				t.Fatalf("Redact does not find %q in the unsplit text, so this case proves nothing", c.secret)
			}
			r, _, _ := newRPC(t, RPCConfig{})
			for _, p := range c.parts {
				r.Tap(RPCIn, chunk("agent_message_chunk", "s1", p))
				if err := r.Flush(); err != nil {
					t.Fatal(err)
				}
			}
			f := read(t, r)
			b, err := os.ReadFile(r.Path())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), c.secret) {
				t.Errorf("the secret reached the file:\n%s", b)
			}
			texts, _ := chunkTexts(f)
			if got := texts["agent_message_chunk/s1"]; !strings.Contains(got, Redacted) {
				t.Errorf("nothing was redacted: %q", got)
			}
		})
	}
}

// Text with nothing secret in it is written as it came, however it is split.
func TestRPCContextDoesNotChangePlainText(t *testing.T) {
	whole := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40) + "end"
	for _, size := range []int{1, 7, 61, 300} {
		r, _, _ := newRPC(t, RPCConfig{})
		for i := 0; i < len(whole); i += size {
			r.Tap(RPCIn, chunk("agent_message_chunk", "s1", whole[i:min(i+size, len(whole))]))
			_ = r.Flush()
		}
		texts, _ := chunkTexts(read(t, r))
		if texts["agent_message_chunk/s1"] != whole {
			t.Errorf("pieces of %d bytes: the text changed:\n%q", size, texts["agent_message_chunk/s1"])
		}
	}
}

func TestRedactAfter(t *testing.T) {
	if got := redactAfter("", "plain"); got != "plain" {
		t.Errorf("no context: %q", got)
	}
	if got := redactAfter("password: ", "Tr0ub4dor&3xyz tail"); strings.Contains(got, "Tr0ub4dor") {
		t.Errorf("value after its label: %q", got)
	}
	if got := redactAfter("a é", "é b"); got != "é b" {
		t.Errorf("multibyte: %q", got)
	}
	long := strings.Repeat("é", rpcContextBytes)
	if got := tailBytes(long+"x", rpcContextBytes); len(got) > rpcContextBytes || got[len(got)-1] != 'x' {
		t.Errorf("tailBytes kept %d bytes", len(got))
	}
}

// A hook that panics on the end of a reply, which is written at Close, drops that
// one line and nothing else: the other held ends and the closing line are written.
func TestRPCHookPanicAtCloseLosesOnlyThatLine(t *testing.T) {
	r, _, _ := newRPC(t, RPCConfig{Hook: func(dir string, msg map[string]any) bool {
		if strings.Contains(chunkTextOf(msg), "boom") {
			panic("hook")
		}
		return true
	}})
	r.Tap(RPCIn, chunk("agent_message_chunk", "s1", "the reply ends in boom boom"))
	r.Tap(RPCIn, chunk("agent_thought_chunk", "s1", "a thought that ends fine now"))
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	f := read(t, r)
	texts, _ := chunkTexts(f)
	if got := texts["agent_thought_chunk/s1"]; got != "a thought that ends fine now" {
		t.Errorf("thought = %q", got)
	}
	if strings.Contains(texts["agent_message_chunk/s1"], "boom") {
		t.Errorf("the line the hook panicked on was written: %q", texts["agent_message_chunk/s1"])
	}
	if last := f.Lines[len(f.Lines)-1]; last.Type != TypeRPCStopped {
		t.Errorf("the recording does not end with its closing line: %s", last.Type)
	}
}

// Retention by count removes the oldest files first, and never one that is still
// being written, whether it is a transcript or a stream recording: each kind of
// start has to see the other kind.
func TestRetentionByCountSparesOpenRecordingsOfBothKinds(t *testing.T) {
	m, _ := newTestManager(t)
	f := newFeed(t, m)
	f.prompt("hello")
	term := f.path()
	r1, err := m.StartRPC(RPCConfig{Meta: testMeta})
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Close()
	folder := strings.TrimSuffix(term, term[strings.LastIndexAny(term, `/\`):])
	// The two open ones are the oldest of more files than the count allows.
	oldest := time.Now().Add(-48 * time.Hour)
	for i, p := range []string{term, r1.Path()} {
		stamp := oldest.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	fresh := time.Now().Add(-time.Hour)
	for i := 0; i < RetainFiles+5; i++ {
		p := folder + string(os.PathSeparator) + "filler-" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + sessionFileExt
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, fresh, fresh); err != nil {
			t.Fatal(err)
		}
	}
	// What retention is given to spare. Removing an open file fails on Windows,
	// so the removals below only show it where that works; this shows it anywhere.
	m.mu.Lock()
	keep := m.protectedLocked("opened")
	m.mu.Unlock()
	for name, p := range map[string]string{"transcript": term, "stream": r1.Path(), "the one just opened": "opened"} {
		if !keep[p] {
			t.Errorf("the open %s is not protected from retention", name)
		}
	}
	// A stream recording starting runs the retention: it must spare the transcript.
	r2, err := m.StartRPC(RPCConfig{Meta: testMeta, Now: func() time.Time { return time.Now().Add(time.Second) }})
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	for name, p := range map[string]string{"transcript": term, "stream": r1.Path()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a stream recording starting removed the open %s: %v", name, err)
		}
	}
	if ents, _ := os.ReadDir(folder); len(ents) > RetainFiles+3 {
		t.Errorf("%d files, the count was not enforced", len(ents))
	}
	// And a transcript starting must spare the stream recordings.
	for _, p := range []string{term, r1.Path()} {
		if err := os.Chtimes(p, oldest, oldest); err != nil {
			t.Fatal(err)
		}
	}
	f2 := newFeed(t, m)
	f2.meta.Conversation = "conv-count-2"
	f2.prompt("again")
	for name, p := range map[string]string{"transcript": term, "stream": r1.Path()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a transcript starting removed the open %s: %v", name, err)
		}
	}
}

// A private key longer than the context kept has its BEGIN line cut off by the
// time the rest arrives. The line is kept while the key has not ended, so the
// rest is redacted as part of it.
func TestRPCRedactsTheRestOfAKeyLongerThanTheContext(t *testing.T) {
	line := "MIIEowIBAAKCAQEAzZ3k8QxY9wP2LmN5\n"
	first := "here it is:\n-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat(line, rpcContextBytes/len(line)+200)
	secret := "ZZtopSecretBodyLine0123456789abcdef"
	// Text after the key, longer than the end that is held back, so the secret is
	// in the part that is written.
	second := strings.Repeat(line, 20) + secret + "\n" + "-----END RSA PRIVATE KEY-----\n" + strings.Repeat("after the key. ", 40)
	r, _, _ := newRPC(t, RPCConfig{})
	for _, p := range []string{first, second} {
		r.Tap(RPCIn, chunk("agent_message_chunk", "s1", p))
		if err := r.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) {
		t.Errorf("the rest of a long key reached the file")
	}
	if !strings.Contains(string(b), "after the key. ") {
		t.Errorf("the text after the key was lost")
	}
}
