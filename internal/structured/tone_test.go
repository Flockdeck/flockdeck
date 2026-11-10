package structured

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// noticeNamed returns the notice entry with this text.
func noticeNamed(t *testing.T, s Snapshot, text string) transcript.Entry {
	t.Helper()
	for _, e := range s.Entries {
		if e.Kind == transcript.KindNotice && e.Text == text {
			return e
		}
	}
	t.Fatalf("no notice %q in %+v", text, s.Entries)
	return transcript.Entry{}
}

// A session that could not be resumed and a fork that could not be made are told
// to the window as failures, through the bridge's Failure, so the window draws them
// apart from a plain note.
func TestResumeFailedAndForkFailedNoticesCarryTheFailureTone(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		r := begin(t, resumeOptions(t, "old-1", acptest.WithKnownSessions("old-1")))
		s := until(t, r, "ready", idle)
		if e := noticeNamed(t, s, ResumeFailedText); e.Tone != transcript.ToneFailure {
			t.Errorf("resume-failed tone %q, want %q", e.Tone, transcript.ToneFailure)
		}
	})
	t.Run("fork", func(t *testing.T) {
		o := fakeOptions(t) // an agent with no fork
		o.Fork = &ForkFrom{Session: "sess-1", At: "at-2"}
		r := begin(t, o)
		s := until(t, r, "ready", idle)
		if e := noticeNamed(t, s, ForkFailedText); e.Tone != transcript.ToneFailure {
			t.Errorf("fork-failed tone %q, want %q", e.Tone, transcript.ToneFailure)
		}
	})
}

// Only SendTyped marks a prompt as the person's typing. The opening prompt, a
// plain Send (a wake or a shell's output) and mail do not, so a window offers
// Retry on the typed one alone.
func TestOnlyTypedTextMakesATypedPrompt(t *testing.T) {
	r := begin(t, fakeOptions(t))
	until(t, r, "ready", idle)
	for _, step := range []struct {
		name string
		send func() error
	}{
		{"system", func() error { return r.SendSystem("plain opening") }},
		{"wake", func() error { return r.Send("plain wake") }},
		{"mail", func() error { return r.SendMail(7, "alice", "plain mail", "plain mail") }},
		{"typed", func() error { return r.SendTyped("plain typed", nil) }},
	} {
		before := len(r.Snapshot().Entries)
		if err := step.send(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		until(t, r, step.name+" done", func(s Snapshot) bool { return idle(s) && lastIsReply(s) && len(s.Entries) > before+1 })
	}
	s := r.Snapshot()
	typed := map[string]bool{}
	for _, e := range s.Entries {
		if e.Kind == transcript.KindPrompt {
			typed[e.Text] = e.Typed
		}
		if e.Kind == transcript.KindMessage && e.Typed {
			t.Errorf("a mail entry is marked typed: %+v", e)
		}
	}
	want := map[string]bool{"plain opening": false, "plain wake": false, "plain typed": true}
	for text, w := range want {
		if got, ok := typed[text]; !ok || got != w {
			t.Errorf("prompt %q typed = %v (present %v), want %v", text, got, ok, w)
		}
	}
	// On the wire the marker is one bool, present only when true.
	b, _ := json.Marshal(transcript.Entry{Kind: transcript.KindPrompt, Text: "x"})
	if strings.Contains(string(b), "typed") {
		t.Errorf("an untyped prompt carries typed on the wire: %s", b)
	}
}

func lastIsReply(s Snapshot) bool {
	n := len(s.Entries)
	return n > 0 && s.Entries[n-1].Kind == transcript.KindReply
}
