package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWakeIsOnByDefaultAndLeftOutOfTheFile(t *testing.T) {
	var p Prefs
	if !p.WakeEnabled("claude", "p1") {
		t.Fatal("wake starts off")
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["wake"]; ok {
		t.Fatalf("a default Wake was written: %s", b)
	}
}

// Any switch that is off wins, and turning one on clears only that scope.
func TestWakePrecedence(t *testing.T) {
	cases := []struct {
		name string
		set  func(p *Prefs)
		// agent and pane are what the pane is asked about.
		agent, pane string
		want        bool
	}{
		{"all on", func(*Prefs) {}, "claude", "p1", true},
		{"global off", func(p *Prefs) { p.SetWake(WakeGlobal, "", false) }, "claude", "p1", false},
		{"type off, same type", func(p *Prefs) { p.SetWake(WakeAgent, "claude", false) }, "claude", "p1", false},
		{"type off, other type", func(p *Prefs) { p.SetWake(WakeAgent, "claude", false) }, "aider", "p1", true},
		{"pane off, same pane", func(p *Prefs) { p.SetWake(WakePane, "p1", false) }, "claude", "p1", false},
		{"pane off, other pane", func(p *Prefs) { p.SetWake(WakePane, "p1", false) }, "claude", "p2", true},
		{"type on does not undo global off", func(p *Prefs) {
			p.SetWake(WakeGlobal, "", false)
			p.SetWake(WakeAgent, "claude", true)
		}, "claude", "p1", false},
		{"global on does not undo pane off", func(p *Prefs) {
			p.SetWake(WakePane, "p1", false)
			p.SetWake(WakeGlobal, "", true)
		}, "claude", "p1", false},
		{"pane back on", func(p *Prefs) {
			p.SetWake(WakePane, "p1", false)
			p.SetWake(WakePane, "p1", true)
		}, "claude", "p1", true},
		{"unknown agent still hit by global", func(p *Prefs) { p.SetWake(WakeGlobal, "", false) }, "", "p1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p Prefs
			c.set(&p)
			if got := p.WakeEnabled(c.agent, c.pane); got != c.want {
				t.Fatalf("WakeEnabled(%q, %q) = %v, want %v", c.agent, c.pane, got, c.want)
			}
		})
	}
}

func TestSetWakeReportsChangesAndRefusesNonsense(t *testing.T) {
	var p Prefs
	if p.SetWake(WakeGlobal, "", true) {
		t.Error("turning on what is on reported a change")
	}
	if !p.SetWake(WakeAgent, "claude", false) || p.SetWake(WakeAgent, "claude", false) {
		t.Error("turning an agent off twice should report a change once")
	}
	for _, bad := range [][2]string{{"", "x"}, {"team", "x"}, {WakeAgent, ""}, {WakePane, ""}} {
		if p.SetWake(bad[0], bad[1], false) {
			t.Errorf("SetWake(%q, %q) changed something", bad[0], bad[1])
		}
	}
	if p.SetWake(WakePane, string(make([]byte, 129)), false) {
		t.Error("a 129 byte id was accepted")
	}
}

// The lists are replaced, so a copy of the Prefs taken before the change does
// not see it.
func TestSetWakeDoesNotWriteIntoACopy(t *testing.T) {
	var p Prefs
	p.SetWake(WakePane, "p1", false)
	copied := p
	p.SetWake(WakePane, "p2", false)
	p.SetWake(WakePane, "p1", true)
	if !reflect.DeepEqual(copied.Wake.Panes, []string{"p1"}) {
		t.Fatalf("the copy changed: %v", copied.Wake.Panes)
	}
	if !reflect.DeepEqual(p.Wake.Panes, []string{"p2"}) {
		t.Fatalf("panes = %v", p.Wake.Panes)
	}
}

func TestWakeListsAreCapped(t *testing.T) {
	var p Prefs
	for i := 0; i < maxWakeOff+10; i++ {
		p.SetWake(WakePane, string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676)), false)
	}
	if len(p.Wake.Panes) != maxWakeOff {
		t.Fatalf("%d panes listed, want %d", len(p.Wake.Panes), maxWakeOff)
	}
}

func TestWakeSwitchesPersistAndLoad(t *testing.T) {
	isolateConfig(t)
	err := UpdatePrefs(func(p *Prefs) bool {
		p.SetWake(WakeGlobal, "", false)
		p.SetWake(WakeAgent, "aider", false)
		return p.SetWake(WakePane, "p9", false)
	})
	if err != nil {
		t.Fatal(err)
	}
	got := LoadPrefs()
	if !got.Wake.Off || !reflect.DeepEqual(got.Wake.Agents, []string{"aider"}) || !reflect.DeepEqual(got.Wake.Panes, []string{"p9"}) {
		t.Fatalf("loaded %+v", got.Wake)
	}
	if got.ForWindow().Wake.Off != true {
		t.Error("a window is not told wake is off")
	}
}
