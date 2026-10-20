package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

func testWakeEnv(inPane bool, events ...team.Event) wakeEnv {
	return wakeEnv{
		inPane: func() bool { return inPane },
		events: func() ([]team.Event, error) { return events, nil },
		// A person at a terminal outside a pane, unless a test says otherwise.
		terminal:   func() bool { return true },
		paneAPI:    func() bool { return false },
		read:       store.ReadWake,
		offersText: func(id string) bool { return id == "aider" },
	}
}

func runW(t *testing.T, env wakeEnv, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runWakeWith(env, args, &out)
	return out.String(), err
}

func TestWakeSwitchesPersist(t *testing.T) {
	isolateState(t)
	env := testWakeEnv(false)
	for _, args := range [][]string{{"off"}, {"off", "-agent", "aider"}, {"off", "-pane", "p3"}} {
		if _, err := runW(t, env, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	w := store.LoadPrefs().Wake
	if !w.Off || len(w.Agents) != 1 || w.Agents[0] != "aider" || len(w.Panes) != 1 || w.Panes[0] != "p3" {
		t.Fatalf("wake = %+v", w)
	}
	out, err := runW(t, env, "on", "-pane", "p3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Still held off") {
		t.Errorf("turning a pane on under a global off did not say so: %q", out)
	}
	if got := store.LoadPrefs().Wake; len(got.Panes) != 0 || !got.Off {
		t.Fatalf("wake = %+v", got)
	}
}

func TestWakeChangesAreRefusedInsideAPane(t *testing.T) {
	isolateState(t)
	for _, args := range [][]string{{"off"}, {"on"}, {"off", "-agent", "claude"}} {
		if _, err := runW(t, testWakeEnv(true), args...); err == nil || !strings.Contains(err.Error(), "inside a pane") {
			t.Errorf("%v inside a pane: err = %v", args, err)
		}
	}
	if w := store.LoadPrefs().Wake; w.Off || len(w.Agents) != 0 {
		t.Fatalf("a refused command changed wake: %+v", w)
	}
	// Reading is allowed in a pane.
	if _, err := runW(t, testWakeEnv(true), "status"); err != nil {
		t.Fatalf("status inside a pane: %v", err)
	}
}

func TestWakeRefusesBadArguments(t *testing.T) {
	isolateState(t)
	env := testWakeEnv(false)
	for _, args := range [][]string{
		{"maybe"},
		{"off", "-agent", "a", "-pane", "p"},
		{"status", "-pane", "p"},
		{"off", "extra"},
	} {
		if _, err := runW(t, env, args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if w := store.LoadPrefs().Wake; w.Off || len(w.Agents) != 0 || len(w.Panes) != 0 {
		t.Fatalf("a bad command changed wake: %+v", w)
	}
}

func TestWakeStatusShowsSwitchesAndTheLastEventsWithoutMessageText(t *testing.T) {
	isolateState(t)
	if err := store.UpdatePrefs(func(p *store.Prefs) bool {
		p.SetWake(store.WakeAgent, "aider", false)
		return p.SetWake(store.WakePane, "p3", false)
	}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	var evs []team.Event
	evs = append(evs, team.Event{Kind: team.EventMessage, ID: 1, Time: at, From: "a", To: "b"})
	for i := 0; i < 4; i++ {
		evs = append(evs, team.Event{Kind: team.EventWake, ID: 10 + i, Time: at.Add(time.Duration(i) * time.Minute),
			Pane: "p1", Agent: "claude", Rule: "1", RuleSource: "built-in", Line: "Flockdeck: new mail, message 10"})
	}
	evs[len(evs)-1].Unsubmitted = true
	out, err := runW(t, testWakeEnv(false, evs...), "status", "-n", "2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"global: on", "agent types off: aider", "panes off: p3", "message 12", "message 13", "Enter not sent"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"message 10", "message 11", "Flockdeck: new mail"} {
		if strings.Contains(out, not) {
			t.Errorf("status has %q:\n%s", not, out)
		}
	}
	// The default view with nothing recorded.
	out, err = runW(t, testWakeEnv(false), "")
	if err == nil {
		t.Fatalf("an empty action was accepted: %q", out)
	}
	out, err = runW(t, testWakeEnv(false))
	if err != nil || !strings.Contains(out, "no wake events") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// on is the change that matters, so it is refused where an agent would run it:
// with no terminal on stdin, or with a pane's API address or token in the
// environment. off is not.
func TestWakeOnIsRefusedWithoutATerminalOrWithAPaneAPI(t *testing.T) {
	isolateState(t)
	if _, err := runW(t, testWakeEnv(false), "off"); err != nil {
		t.Fatal(err)
	}
	noTTY := testWakeEnv(false)
	noTTY.terminal = func() bool { return false }
	withAPI := testWakeEnv(false)
	withAPI.paneAPI = func() bool { return true }
	for name, env := range map[string]wakeEnv{"no terminal": noTTY, "pane API in the environment": withAPI} {
		for _, args := range [][]string{{"on"}, {"on", "-agent", "claude"}, {"on", "-pane", "p1"}} {
			if _, err := runW(t, env, args...); err == nil {
				t.Errorf("%s: %v was accepted", name, args)
			}
		}
		if !store.LoadPrefs().Wake.Off {
			t.Fatalf("%s: a refused on turned wake on", name)
		}
	}
	// Turning things off needs neither.
	if _, err := runW(t, noTTY, "off", "-agent", "claude"); err != nil {
		t.Errorf("off without a terminal: %v", err)
	}
	if _, err := runW(t, withAPI, "off", "-pane", "p1"); err != nil {
		t.Errorf("off with a pane API: %v", err)
	}
	// And a person at a terminal outside a pane can turn it on.
	if _, err := runW(t, testWakeEnv(false), "on"); err != nil {
		t.Fatal(err)
	}
	if store.LoadPrefs().Wake.Off {
		t.Fatal("on did not take")
	}
}

func TestWakeStatusSaysWhenThePreferencesAreDamaged(t *testing.T) {
	isolateState(t)
	t.Cleanup(store.ClearWakeDamaged)
	env := testWakeEnv(false)
	env.read = func() (store.Prefs, error) { return store.Prefs{}, store.ErrPrefsDamaged }
	out, err := runW(t, env, "status")
	if err != nil || !strings.Contains(out, "preferences damaged, wake is off") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	env.read = func() (store.Prefs, error) { return store.Prefs{}, errors.New("disk on fire") }
	if _, err := runW(t, env, "status"); err == nil || !strings.Contains(err.Error(), "wake is off") {
		t.Fatalf("an unreadable file was reported as %v", err)
	}
}

// The global switch set from the CLI lifts the damaged state a moved-aside file left.
func TestWakeCommandLiftsTheDamagedMarker(t *testing.T) {
	isolateState(t)
	t.Cleanup(store.ClearWakeDamaged)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prefs.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.ReadPrefs()
	if _, err := store.ReadWake(); !errors.Is(err, store.ErrPrefsDamaged) {
		t.Fatalf("not damaged: %v", err)
	}
	if _, err := runW(t, testWakeEnv(false), "on"); err != nil {
		t.Fatal(err)
	}
	if _, err := runW(t, testWakeEnv(false), "off", "-pane", "p1"); err != nil {
		t.Fatal(err)
	}
	p, err := store.ReadWake()
	if err != nil || p.WakeEnabled("claude", "p1") || !p.WakeEnabled("claude", "p2") {
		t.Fatalf("err = %v, wake = %+v", err, p.Wake)
	}
}

// damagePrefs writes a preferences file with the user's global off in it and a
// bad value elsewhere, and reads it as the rest of the program would, which
// moves it aside and leaves the marker.
func damagePrefs(t *testing.T) {
	t.Helper()
	t.Cleanup(store.ClearWakeDamaged)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prefs.json"), []byte(`{"wake":{"off":true},"theme":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store.ReadPrefs()
	if _, err := store.ReadWake(); !errors.Is(err, store.ErrPrefsDamaged) {
		t.Fatalf("not damaged: %v", err)
	}
}

// A switch for one pane or agent type does not lift the hold a damaged file
// has on wake, and says so. Only the global switch does, even when it changes
// nothing.
func TestOnlyTheGlobalSwitchLiftsTheDamagedHold(t *testing.T) {
	isolateState(t)
	damagePrefs(t)
	for _, args := range [][]string{{"off", "-pane", "p9"}, {"off", "-agent", "aider"}, {"on", "-pane", "p9"}} {
		out, err := runW(t, testWakeEnv(false), args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "still off everywhere") {
			t.Errorf("%v did not say wake is still held off: %q", args, out)
		}
		if _, err := store.ReadWake(); !errors.Is(err, store.ErrPrefsDamaged) {
			t.Fatalf("%v lifted the hold: err = %v", args, err)
		}
	}
	// The global switch, here "on" when everything already reads as on.
	if _, err := runW(t, testWakeEnv(false), "on"); err != nil {
		t.Fatal(err)
	}
	if p, err := store.ReadWake(); err != nil || !p.WakeEnabled("claude", "p1") {
		t.Fatalf("after the global switch: %v", err)
	}
}

func TestWakeTerminalRefusesTheNullDevice(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	orig := os.Stdin
	os.Stdin = null
	defer func() { os.Stdin = orig }()
	if wakeTerminal() {
		t.Fatal("the null device passed for a terminal")
	}
}

// A scoped switch leaves the marker file itself in place, not only the
// reading of it.
func TestScopedSwitchLeavesTheDamagedMarkerFile(t *testing.T) {
	isolateState(t)
	damagePrefs(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "prefs.wake-damaged")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no marker after the file was damaged: %v", err)
	}
	if _, err := runW(t, testWakeEnv(false), "off", "-pane", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a pane switch removed the marker: %v", err)
	}
}

// wakeTerminal is false for a pipe and for a file, as well as the null device.
func TestWakeTerminalRefusesAPipeAndAFile(t *testing.T) {
	orig := os.Stdin
	defer func() { os.Stdin = orig }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	os.Stdin = r
	if wakeTerminal() {
		t.Fatal("a pipe passed for a terminal")
	}

	f, err := os.Create(filepath.Join(t.TempDir(), "in"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdin = f
	if wakeTerminal() {
		t.Fatal("a file passed for a terminal")
	}
}

func TestWakeTextIsOptInPerAgentAndOnlyWhereOffered(t *testing.T) {
	isolateState(t)
	env := testWakeEnv(false)
	if p := store.LoadPrefs(); p.WakeText("aider") {
		t.Fatal("text mode starts on")
	}
	if _, err := runW(t, env, "text", "on", "-agent", "claude"); err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("an agent that does not offer it was opted in: %v", err)
	}
	if store.LoadPrefs().WakeText("claude") {
		t.Fatal("claude was opted in")
	}
	if _, err := runW(t, env, "text", "on", "-agent", "aider"); err != nil {
		t.Fatal(err)
	}
	if !store.LoadPrefs().WakeText("aider") {
		t.Fatal("aider was not opted in")
	}
	out, err := runW(t, env, "status")
	if err != nil || !strings.Contains(out, "message text typed for: aider") {
		t.Fatalf("status = %q, %v", out, err)
	}
	// Off is always allowed, even for an agent that no longer offers it.
	if _, err := runW(t, env, "text", "off", "-agent", "aider"); err != nil {
		t.Fatal(err)
	}
	if store.LoadPrefs().WakeText("aider") {
		t.Fatal("aider is still opted in")
	}
}

func TestWakeTextRefusesBadArgumentsAndPanes(t *testing.T) {
	isolateState(t)
	for _, args := range [][]string{
		{"text"}, {"text", "on"}, {"text", "maybe", "-agent", "aider"}, {"text", "on", "-pane", "p1"},
	} {
		if _, err := runW(t, testWakeEnv(false), args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if _, err := runW(t, testWakeEnv(true), "text", "on", "-agent", "aider"); err == nil || !strings.Contains(err.Error(), "inside a pane") {
		t.Errorf("text on inside a pane: %v", err)
	}
	if store.LoadPrefs().WakeText("aider") {
		t.Fatal("a refused command changed the setting")
	}
}

// text on is held to the same checks as on, one by one: the terminal, the
// pane API in the environment, and a pane. text off is not.
func TestWakeTextOnIsHeldToTheChecksOfOn(t *testing.T) {
	isolateState(t)
	noTTY := testWakeEnv(false)
	noTTY.terminal = func() bool { return false }
	withAPI := testWakeEnv(false)
	withAPI.paneAPI = func() bool { return true }
	for name, env := range map[string]wakeEnv{"no terminal": noTTY, "pane API in the environment": withAPI} {
		if _, err := runW(t, env, "text", "on", "-agent", "aider"); err == nil {
			t.Errorf("%s: text on was accepted", name)
		}
		if store.LoadPrefs().WakeText("aider") {
			t.Fatalf("%s: a refused text on opted aider in", name)
		}
	}
	if _, err := runW(t, testWakeEnv(false), "text", "on", "-agent", "aider"); err != nil {
		t.Fatal(err)
	}
	// Turning it off needs neither.
	if _, err := runW(t, noTTY, "text", "off", "-agent", "aider"); err != nil {
		t.Errorf("text off without a terminal: %v", err)
	}
	if store.LoadPrefs().WakeText("aider") {
		t.Fatal("text off did not take")
	}
}

func TestWakeTextRefusesAPaneAndAgentTogether(t *testing.T) {
	isolateState(t)
	for _, args := range [][]string{
		{"text", "on", "-agent", "aider", "-pane", "p1"},
		{"text", "off", "-agent", "aider", "-pane", "p1"},
	} {
		if _, err := runW(t, testWakeEnv(false), args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if store.LoadPrefs().WakeText("aider") {
		t.Fatal("a refused command changed the setting")
	}
}
