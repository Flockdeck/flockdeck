package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// Every way a preferences file can be damaged turns wake off, with the real
// ReadPrefs run on the same file to show what the old reader did: it gave the
// defaults, which turn wake on, and moved the file aside.
func TestReadWakeFailsClosedOnDamage(t *testing.T) {
	cases := map[string]string{
		"truncated":                `{"wake":{"off":tr`,
		"garbage":                  `not json at all`,
		"empty file":               ``,
		"a list":                   `[1,2]`,
		"wake is a string":         `{"wake":"off"}`,
		"off is a string":          `{"wake":{"off":"yes"}}`,
		"agents is a string":       `{"wake":{"agents":"claude"}}`,
		"panes has a number":       `{"wake":{"panes":[1]}}`,
		"a valid switch with junk": `{"wake":{"off":true,"agents":"claude"}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			isolateConfig(t)
			t.Cleanup(ClearWakeDamaged)
			ClearWakeDamaged()
			write := func() string {
				dir, err := Dir()
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, prefsFile)
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			path := write()

			p, err := ReadWake()
			if !errors.Is(err, ErrPrefsDamaged) {
				t.Fatalf("ReadWake err = %v, want ErrPrefsDamaged", err)
			}
			_ = p
			if _, statErr := os.Stat(path); statErr != nil {
				t.Fatal("ReadWake moved the damaged file aside")
			}

			// The reader that turns wake on: defaults, no error, file gone.
			old, oldErr := ReadPrefs()
			if oldErr != nil || !old.WakeEnabled("claude", "p1") {
				t.Fatalf("ReadPrefs did not give the defaults (%v)", oldErr)
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Fatal("ReadPrefs left the damaged file in place")
			}
			// Wake is still off after that.
			if _, err := ReadWake(); !errors.Is(err, ErrPrefsDamaged) {
				t.Fatalf("after ReadPrefs moved the file aside, ReadWake err = %v", err)
			}
			ClearWakeDamaged()
			if p, err := ReadWake(); err != nil || !p.WakeEnabled("claude", "p1") {
				t.Fatalf("after the user set a switch: %v", err)
			}
		})
	}
}

// A bad value elsewhere in the file leaves the wake switches readable.
func TestReadWakeKeepsTheSwitchesWhenAnotherFieldIsBad(t *testing.T) {
	isolateConfig(t)
	t.Cleanup(ClearWakeDamaged)
	ClearWakeDamaged()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte(`{"wake":{"off":true,"panes":["p1"]},"theme":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := ReadWake()
	if err != nil {
		t.Fatal(err)
	}
	if !p.Wake.Off || len(p.Wake.Panes) != 1 {
		t.Fatalf("wake = %+v", p.Wake)
	}
}

func TestReadWakeOfAMissingFileIsAllOn(t *testing.T) {
	isolateConfig(t)
	ClearWakeDamaged()
	p, err := ReadWake()
	if err != nil || !p.WakeEnabled("claude", "p1") {
		t.Fatalf("err = %v", err)
	}
}

// A file that is the JSON null is accepted by ReadPrefs as empty preferences.
// For wake it is not a file anyone wrote switches in, so it fails closed.
func TestReadWakeOfNullFailsClosed(t *testing.T) {
	isolateConfig(t)
	t.Cleanup(ClearWakeDamaged)
	ClearWakeDamaged()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte(`null`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWake(); !errors.Is(err, ErrPrefsDamaged) {
		t.Fatalf("err = %v", err)
	}
}

// The damaged state outlives the process: after a restart the damaged file is
// gone and the marker beside it is what keeps wake off. A switch set since
// clears it.
func TestWakeStaysOffAcrossARestartAfterADamagedFile(t *testing.T) {
	isolateConfig(t)
	t.Cleanup(ClearWakeDamaged)
	ClearWakeDamaged()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefsFile), []byte(`{"wake":{"off":true`), 0o600); err != nil {
		t.Fatal(err)
	}
	ReadPrefs() // moves the file aside and leaves the marker
	if _, err := os.Stat(filepath.Join(dir, wakeDamagedFile)); err != nil {
		t.Fatalf("no marker: %v", err)
	}

	// A restart: nothing in memory, the same directory.
	wakeDamaged.Store(false)
	if _, err := ReadWake(); !errors.Is(err, ErrPrefsDamaged) {
		t.Fatalf("after a restart ReadWake err = %v, want ErrPrefsDamaged", err)
	}
	// Other saves of preferences do not lift it.
	if err := SavePrefs(Prefs{Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWake(); !errors.Is(err, ErrPrefsDamaged) {
		t.Fatalf("after another setting was saved, ReadWake err = %v", err)
	}
	// A wake switch set since does.
	ClearWakeDamaged()
	if p, err := ReadWake(); err != nil || !p.WakeEnabled("claude", "p1") {
		t.Fatalf("after a switch was set: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, wakeDamagedFile)); err == nil {
		t.Fatal("the marker is still there")
	}
}
