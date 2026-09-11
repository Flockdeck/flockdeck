package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadConfigMissing: no file is not a failure, it is a machine where
// nobody has defined an agent yet.
func TestReadConfigMissing(t *testing.T) {
	f, err := ReadConfig(t.TempDir())
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if len(f.Agents) != 0 || f.Defaults != (Defaults{}) {
		t.Errorf("an absent file should read as an empty one, got %+v", f)
	}
}

// TestSetDefaults covers the picker writing a choice back: the installation's
// default, a project's own, and the removal of one.
func TestSetDefaults(t *testing.T) {
	tests := []struct {
		name    string
		project string
		set     Defaults
		check   func(t *testing.T, f *File)
	}{
		{
			name: "the installation's default",
			set:  Defaults{Agent: "codex", Model: "gpt-5"},
			check: func(t *testing.T, f *File) {
				if f.Defaults != (Defaults{Agent: "codex", Model: "gpt-5"}) {
					t.Errorf("defaults = %+v", f.Defaults)
				}
			},
		},
		{
			name:    "this project only",
			project: filepath.Join("work", "api"),
			set:     Defaults{Agent: "gemini"},
			check: func(t *testing.T, f *File) {
				got := f.Projects[filepath.Join("work", "api")]
				if got.Agent != "gemini" {
					t.Errorf("projects = %+v", f.Projects)
				}
				if f.Defaults.Agent != "claude" {
					t.Error("a project default must not disturb the installation's")
				}
			},
		},
		{
			name:    "clearing a project's answer removes it",
			project: filepath.Join("work", "api"),
			set:     Defaults{},
			check: func(t *testing.T, f *File) {
				if _, still := f.Projects[filepath.Join("work", "api")]; still {
					t.Errorf("projects = %+v, want the entry gone", f.Projects)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			// Everything the user already had is written first, because the
			// point of the round trip is that setting one field keeps it all.
			seed := `{
				"version": 1,
				"defaults": {"agent": "claude"},
				"projects": {"` + filepath.ToSlash(filepath.Join("work", "api")) + `": {"agent": "codex"}},
				"agents": [{"id": "local", "runner": "api", "api": {"baseURL": "http://127.0.0.1:11434/v1"}}],
				"somethingNewer": {"kept": true}
			}`
			write(t, dir, seed)

			if err := SetDefaults(dir, tt.project, tt.set); err != nil {
				t.Fatalf("SetDefaults: %v", err)
			}
			f, err := ReadConfig(dir)
			if err != nil {
				t.Fatalf("ReadConfig: %v", err)
			}
			if len(f.Agents) != 1 || !strings.Contains(string(f.Agents[0]), `"local"`) {
				t.Errorf("the user's own agents should survive a defaults write, got %s", f.Agents)
			}
			if _, kept := f.Extra["somethingNewer"]; !kept {
				t.Error("a key this build does not know about must not be deleted")
			}
			tt.check(t, f)
		})
	}
}

// TestSetDefaultsProjectSpelling: the same directory reaches Flockdeck spelled more
// than one way, and two entries for one project would sooner or later
// disagree.
func TestSetDefaultsProjectSpelling(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join("work", "api")
	if err := SetDefaults(dir, project, Defaults{Agent: "codex"}); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}
	if err := SetDefaults(dir, project+string(filepath.Separator), Defaults{Agent: "gemini"}); err != nil {
		t.Fatalf("SetDefaults: %v", err)
	}
	f, err := ReadConfig(dir)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if len(f.Projects) != 1 {
		t.Fatalf("projects = %+v, want one entry", f.Projects)
	}
	c := Merge(f)
	if got := c.DefaultsFor(project); got.Agent != "gemini" {
		t.Errorf("default for the project = %q, want the answer just given", got.Agent)
	}
}

// TestSetDefaultsLeavesAnUnreadableFileAlone. The file is the only copy of
// whatever agents the user defined, and a changed default is nowhere near
// worth losing them: it is better to say so and leave it to be repaired.
func TestSetDefaultsLeavesAnUnreadableFileAlone(t *testing.T) {
	dir := t.TempDir()
	const damaged = `{"agents": [ }`
	write(t, dir, damaged)
	if err := SetDefaults(dir, "", Defaults{Agent: "codex"}); err == nil {
		t.Fatal("writing over a file that could not be read should fail")
	}
	data, err := os.ReadFile(filepath.Join(dir, ConfigName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != damaged {
		t.Errorf("the file was changed: %s", data)
	}
}

// TestFileRoundTripsUnknownFields pins the marshalling on its own, because the
// preservation above would also pass if the file were simply never rewritten.
func TestFileRoundTripsUnknownFields(t *testing.T) {
	var f File
	const in = `{"version":1,"agents":[{"id":"x"}],"later":[1,2,3]}`
	if err := json.Unmarshal([]byte(in), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal back: %v", err)
	}
	for _, name := range []string{"version", "agents", "later"} {
		if _, ok := back[name]; !ok {
			t.Errorf("%q did not survive the round trip: %s", name, out)
		}
	}
}
