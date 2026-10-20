package structured

import (
	"context"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// An error answering a prompt that is not an exit, a login or a close ends that
// turn with a notice and leaves the session ready: the prompts behind it still
// run.
func TestARefusedPromptEndsOnlyTheTurn(t *testing.T) {
	r := begin(t, fakeOptions(t))
	for _, p := range []string{"ratelimit", "plain"} {
		if err := r.Send(p); err != nil {
			t.Fatal(err)
		}
	}
	s := until(t, r, "the second prompt to run", func(s Snapshot) bool {
		return idle(s) && strings.Contains(replies(s), "Hello world")
	})
	if !strings.Contains(notices(s), "Turn ended: failed: rpc error -32603: rate limit exceeded") {
		t.Errorf("the failed turn left no notice: %q", notices(s))
	}
	if got := prompts(s); !slices.Equal(got, []string{"ratelimit", "plain"}) {
		t.Errorf("prompts = %v; the queued prompt should not be dropped", got)
	}
	if s.Phase != PhaseReady || !strings.Contains(s.Err, "rate limit") {
		t.Errorf("phase %q, err %q", s.Phase, s.Err)
	}
	// And the session still takes more.
	if err := r.Send("plain"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "a third prompt", func(s Snapshot) bool {
		return idle(s) && strings.Count(replies(s), "Hello world") == 2
	})
}

func dirty(extra string) string {
	return "a" + string(rune(0x1b)) + "[2J" + string(rune(7)) + "b" + string(rune(0x202e)) + "c" + string(rune(0x200b)) + "d" + extra
}

// Everything the agent says in a setup state is cleaned before it is kept.
func TestSetupTextFromTheAgentIsCleaned(t *testing.T) {
	r := &Runtime{
		ctx:   context.Background(),
		phase: PhaseStarting,
		subs:  map[int]chan struct{}{},
		br:    bridge.New(bridge.Config{}),
		opts:  Options{Entry: Entry{Login: dirty("")}},
	}
	r.fail(nil, &acp.AuthRequiredError{
		Method:  "session/new",
		Message: dirty(" message"),
		Methods: []acp.AuthMethod{{ID: dirty(" id"), Name: dirty(" name"), Description: dirty(" description")}},
	})
	s := r.Snapshot()
	if s.Phase != PhaseSetup || s.Setup == nil || len(s.Setup.Methods) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	m := s.Setup.Methods[0]
	for what, got := range map[string]string{"id": m.ID, "name": m.Name, "description": m.Description, "message": s.Setup.Message, "login": s.Setup.Login, "err": s.Err} {
		if strings.ContainsAny(got, "\x1b\x07") || strings.ContainsRune(got, 0x202e) || strings.ContainsRune(got, 0x200b) {
			t.Errorf("%s was kept with control or format characters: %q", what, got)
		}
		if !strings.HasPrefix(got, "a[2Jbcd") {
			t.Errorf("%s = %q, want the text cleaned and otherwise kept", what, got)
		}
	}
}

func TestCleanRemovesFormatCharacters(t *testing.T) {
	if got := clean("zero"+string(rune(0x200b))+"width"+string(rune(0xfeff))+" "+string(rune(0x202e))+"bidi", 100); got != "zerowidth bidi" {
		t.Errorf("clean = %q", got)
	}
}

// An exit shows the last three lines the agent wrote, not only the last.
func TestExitTextKeepsTheLastThreeLines(t *testing.T) {
	got := exitText("one\ntwo\n\nthree\n  four  \nfive\n")
	if got != "the agent exited: three / four / five" {
		t.Errorf("exitText = %q", got)
	}
	if got := exitText("only"); got != "the agent exited: only" {
		t.Errorf("exitText = %q", got)
	}
	long := strings.Repeat("a", 200) + "\n" + strings.Repeat("b", 200) + "\n" + strings.Repeat("c", 200)
	got = exitText(long)
	body := strings.TrimPrefix(got, "the agent exited: ")
	if n := len([]rune(body)); n > errCap+1 {
		t.Errorf("%d runes, over the cap of %d", n, errCap)
	}
	if !strings.HasSuffix(body, strings.Repeat("c", 200)) || !strings.HasPrefix(body, "…") {
		t.Errorf("the end of the text should be kept: %q", body)
	}
}

// Case counts on Linux and macOS and does not on Windows.
func TestPassEnvCaseFollowsThePlatform(t *testing.T) {
	got := Entry{PassEnv: []string{"home"}}.passEnv([]string{"HOME=a", "Home=b"})
	want := []string{}
	if runtime.GOOS == "windows" {
		want = []string{"HOME=a", "Home=b"}
	}
	if !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Errorf("on %s got %v, want %v", runtime.GOOS, got, want)
	}
	got = Entry{PassEnv: []string{"HOME"}}.passEnv([]string{"HOME=a", "Home=b"})
	wantExact := []string{"HOME=a"}
	if runtime.GOOS == "windows" {
		wantExact = []string{"HOME=a", "Home=b"}
	}
	if !slices.Equal(got, wantExact) {
		t.Errorf("on %s got %v, want %v", runtime.GOOS, got, wantExact)
	}
}
