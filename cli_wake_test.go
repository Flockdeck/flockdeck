package main

import (
	"bytes"
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
