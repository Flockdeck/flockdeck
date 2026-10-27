package structured

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

func setLog(t *testing.T) (string, acptest.Option) {
	p := filepath.Join(t.TempDir(), "set.log")
	return p, acptest.WithSettingLog(p)
}

func sent(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

func ready(t *testing.T, r *Runtime) Snapshot {
	return until(t, r, "ready", func(s Snapshot) bool {
		return s.Phase == PhaseReady && (s.Session.Modes != nil || len(s.Session.ConfigOptions) > 0)
	})
}

func TestModeAndModelAreSetAndShownInTheSnapshot(t *testing.T) {
	log, opt := setLog(t)
	r := begin(t, fakeOptions(t, acptest.WithModes(), opt))
	ready(t, r)
	if err := r.SetMode("yolo"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := r.SetModel("gem-flash"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	s := r.Snapshot().Session
	if s.Modes.Current != "yolo" || s.Models.Current != "gem-flash" {
		t.Errorf("mode %q model %q, want yolo and gem-flash", s.Modes.Current, s.Models.Current)
	}
	if got := sent(t, log); !strings.Contains(got, `"modeId":"yolo"`) || !strings.Contains(got, `"modelId":"gem-flash"`) {
		t.Errorf("the agent was sent %q", got)
	}
}

// A value that is not in the lists the pane pushed never reaches the agent.
func TestAValueNotOnOfferIsRefusedWithoutAskingTheAgent(t *testing.T) {
	log, opt := setLog(t)
	r := begin(t, fakeOptions(t, acptest.WithModes(), opt))
	ready(t, r)
	for name, err := range map[string]error{
		"mode":                    r.SetMode("bypass"),
		"model":                   r.SetModel("gem-ultra"),
		"empty":                   r.SetMode(""),
		"config on a modes agent": r.SetConfig("model", "big"),
	} {
		if !errors.Is(err, ErrNoSuchSetting) {
			t.Errorf("%s: err = %v, want ErrNoSuchSetting", name, err)
		}
	}
	if got := sent(t, log); got != "" {
		t.Errorf("the agent was sent %q", got)
	}
	if m := r.Snapshot().Session.Modes.Current; m != "default" {
		t.Errorf("mode = %q after refused changes", m)
	}
}

func TestConfigOptionsAreSetAndStaleOnesRefused(t *testing.T) {
	log, opt := setLog(t)
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions(), acptest.WithBooleanOption(), opt))
	ready(t, r)
	if err := r.SetConfig("model", "small"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := r.SetConfig("fast", "true"); err != nil {
		t.Fatalf("SetConfig boolean: %v", err)
	}
	cur := map[string]string{}
	for _, o := range r.Snapshot().Session.ConfigOptions {
		cur[o.ID] = o.Current
	}
	if cur["model"] != "small" || cur["fast"] != "true" {
		t.Errorf("current values = %v", cur)
	}
	for _, c := range [][2]string{{"model", "huge"}, {"nope", "big"}, {"fast", "maybe"}, {"mode", ""}} {
		if err := r.SetConfig(c[0], c[1]); !errors.Is(err, ErrNoSuchSetting) {
			t.Errorf("SetConfig(%q, %q) = %v, want ErrNoSuchSetting", c[0], c[1], err)
		}
	}
	if n := strings.Count(sent(t, log), "\n"); n != 2 {
		t.Errorf("the agent got %d requests, want 2", n)
	}
}

// The state prompt makes the agent replace its options with one; the option
// the person was looking at is gone, so a change to it is stale.
func TestAnOptionTheAgentReplacedIsStale(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions()))
	ready(t, r)
	if err := r.Send("state"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the replaced options", func(s Snapshot) bool { return idle(s) && len(s.Session.ConfigOptions) == 1 })
	if err := r.SetConfig("mode", "plan"); !errors.Is(err, ErrNoSuchSetting) {
		t.Errorf("err = %v, want ErrNoSuchSetting", err)
	}
}

func TestABareReplyKeepsTheOptionsWithTheChange(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithConfigOptions(), acptest.WithBareSettingReplies()))
	ready(t, r)
	if err := r.SetConfig("mode", "plan"); err != nil {
		t.Fatal(err)
	}
	opts := r.Snapshot().Session.ConfigOptions
	if len(opts) != 2 || opts[1].ID != "mode" || opts[1].Current != "plan" {
		t.Errorf("options = %+v", opts)
	}
}

func TestAnAgentErrorIsFixedTextAndChangesNothing(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes(), acptest.WithRefusedSettings()))
	ready(t, r)
	err := r.SetMode("yolo")
	if !errors.Is(err, ErrSettingFailed) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("err = %v, want the fixed ErrSettingFailed", err)
	}
	if m := r.Snapshot().Session.Modes.Current; m != "default" {
		t.Errorf("mode = %q after the agent refused", m)
	}
	if s := r.Snapshot(); strings.Contains(s.Err, "SECRET") {
		t.Errorf("the agent's text reached the snapshot: %q", s.Err)
	}
}

func TestSettingsNeedARunningAgent(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes()))
	ready(t, r)
	_ = r.Close()
	if err := r.SetMode("yolo"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("SetMode after Close = %v, want ErrNotRunning", err)
	}
	if err := r.SetConfig("model", "big"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("SetConfig after Close = %v, want ErrNotRunning", err)
	}
}

// Setting requests that wait on the agent are bounded, so a caller cannot pile
// up goroutines behind a slow agent.
func TestSettingRequestsWaitingOnTheAgentAreBounded(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes(), acptest.WithSlowSettings()))
	ready(t, r)
	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- r.SetMode("yolo") }()
	}
	ok, refused := 0, 0
	for i := 0; i < n; i++ {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrSettingFailed):
			refused++
		default:
			t.Errorf("err = %v", err)
		}
	}
	if ok != maxSettingCalls || refused != n-maxSettingCalls {
		t.Errorf("%d took and %d were refused; want %d and %d", ok, refused, maxSettingCalls, n-maxSettingCalls)
	}
}

// An agent that takes a mode and says nothing more is shown in the new mode.
func TestAModeTheAgentTookIsShownWithoutAnUpdate(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes(), acptest.WithQuietModeChanges()))
	ready(t, r)
	if err := r.SetMode("yolo"); err != nil {
		t.Fatal(err)
	}
	if m := r.Snapshot().Session.Modes.Current; m != "yolo" {
		t.Errorf("mode = %q, want yolo", m)
	}
}

func TestLooksUnasked(t *testing.T) {
	for _, c := range []struct {
		id, name, desc string
		want           bool
	}{
		{"yolo", "YOLO", "", true},
		{"bypassPermissions", "", "", true},
		{"full-access", "Full access", "Never ask, no sandbox", true},
		{"x", "Custom", "Runs everything without asking", true},
		{"dontAsk", "", "", true},
		{"default", "Default", "Asks before it edits", false},
		{"plan", "Plan", "Plan only; no edits or commands", false},
		{"read-only", "Read only", "Ask before anything that changes them", false},
		{"acceptEdits", "Accept edits", "Edit files without asking; still ask for commands", false},
		{"auto", "Auto", "Edit files in the project and run commands there; ask before anything else", false},
	} {
		if got := LooksUnasked(c.id, c.name, c.desc); got != c.want {
			t.Errorf("LooksUnasked(%q, %q, %q) = %v, want %v", c.id, c.name, c.desc, got, c.want)
		}
	}
}

// The unasked marking follows the mode: a mode that runs without asking turns it
// on, and going back to an asking one turns it off.
func TestUnaskedFollowsTheMode(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes()))
	ready(t, r)
	if r.Unasked() {
		t.Fatal("the default mode reads as unasked")
	}
	if err := r.SetMode("yolo"); err != nil {
		t.Fatal(err)
	}
	if !r.Unasked() {
		t.Error("yolo is not marked as running without asking")
	}
	if err := r.SetMode("default"); err != nil {
		t.Fatal(err)
	}
	if r.Unasked() {
		t.Error("the mark stayed after the mode went back")
	}
}

// A request the agent never answers ends at the timeout and gives its slot back.
func TestAnUnansweredSettingEndsAtTheTimeoutAndFreesItsSlot(t *testing.T) {
	o := fakeOptions(t, acptest.WithModes(), acptest.WithSilentSettings())
	o.Client = func(c *acp.Config) { c.SetupTimeout = 300 * time.Millisecond }
	r := begin(t, o)
	ready(t, r)
	for i := 0; i < maxSettingCalls+2; i++ {
		start := time.Now()
		if err := r.SetMode("yolo"); !errors.Is(err, ErrSettingFailed) {
			t.Fatalf("call %d: err = %v", i, err)
		}
		// A refusal for want of a slot would come at once.
		if d := time.Since(start); d < 200*time.Millisecond {
			t.Fatalf("call %d returned after %v, before the timeout", i, d)
		}
	}
	r.mu.Lock()
	n := r.settingCalls
	r.mu.Unlock()
	if n != 0 {
		t.Errorf("%d slots are still held", n)
	}
}

func TestClosingTheRuntimeEndsACallInFlight(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes(), acptest.WithSilentSettings()))
	ready(t, r)
	done := make(chan error, 1)
	go func() { done <- r.SetMode("yolo") }()
	time.Sleep(300 * time.Millisecond)
	_ = r.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Error("the call succeeded on a closed runtime")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not end when the runtime was closed")
	}
	r.mu.Lock()
	n := r.settingCalls
	r.mu.Unlock()
	if n != 0 {
		t.Errorf("%d slots are still held", n)
	}
}

func TestASessionThatChangedIsNotTheOneChecked(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithModes()))
	ready(t, r)
	c, sid, err := r.live()
	if err != nil {
		t.Fatal(err)
	}
	defer r.settingDone()
	if !r.same(c, sid) || r.same(c, sid+"-other") || r.same(nil, sid) {
		t.Error("same does not tell the session it checked from another")
	}
}
