package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestConfigWithAByteOrderMarkIsRead is about the editors Windows ships with,
// which save UTF-8 with a byte-order mark in front of it.
func TestConfigWithAByteOrderMarkIsRead(t *testing.T) {
	dir := t.TempDir()
	data := []byte("\xef\xbb\xbf{\"defaults\": {\"agent\": \"codex\"}}")
	if err := os.WriteFile(filepath.Join(dir, ConfigName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	c := LoadFrom(dir)
	if c.Notice != "" || c.DefaultsFor("").Agent != "codex" {
		t.Errorf("notice %q, default agent %q; want the file read as written", c.Notice, c.DefaultsFor("").Agent)
	}
	if err := SetDefaults(dir, "", Defaults{Agent: "claude"}); err != nil {
		t.Errorf("a default could not be saved over it: %v", err)
	}
}

// TestConcurrentDefaultsAreAllKept: two saves at once each read the file,
// changed their own entry and wrote it back, so one of them was lost -- and
// on Windows a rename over a file the other had open failed outright.
func TestConcurrentDefaultsAreAllKept(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			if err := SetDefaults(dir, fmt.Sprintf("/work/p%d", i), Defaults{Agent: "codex"}); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	f, err := ReadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Projects) != 20 {
		t.Errorf("%d project defaults kept of 20", len(f.Projects))
	}
}

// TestSavingADefaultKeepsTheUsersText: the file is hand-written, and saving a
// default from the picker turned every "&", "<" and ">" in it into a \u
// escape -- a baseURL's query string among them.
func TestSavingADefaultKeepsTheUsersText(t *testing.T) {
	dir := t.TempDir()
	src := `{"version": 1, "agents": [{"id": "gw", "api": {"baseURL": "https://gw.example/v1?a=1&b=<2>"}, "install": "pip install gw && gw login"}], "later": {"note": "a & b"}}`
	if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaults(dir, "", Defaults{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"?a=1&b=<2>", "pip install gw && gw login", "a & b"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%q did not survive a save:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), `\u00`) {
		t.Errorf("a save escaped the user's text:\n%s", data)
	}
}

// TestAnEmptyConfigIsNoConfig: an empty file holds nothing to lose, and as a
// parse error it stopped the picker saving any default into it.
func TestAnEmptyConfigIsNoConfig(t *testing.T) {
	for _, body := range []string{"", "  \r\n", "\xef\xbb\xbf"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if c := LoadFrom(dir); c.Notice != "" {
			t.Errorf("%q: notice %q, want none", body, c.Notice)
		}
		if err := SetDefaults(dir, "", Defaults{Agent: "codex"}); err != nil {
			t.Errorf("%q: a default could not be saved: %v", body, err)
		}
	}
}

// TestAParseErrorSaysWhereInTheFile: encoding/json gives a byte offset, or
// for a file of the wrong shape a type name from inside this package.
func TestAParseErrorSaysWhereInTheFile(t *testing.T) {
	for body, want := range map[string]string{
		"{\n  \"version\": 1,\n  \"agents\": [\n    {\"id\": \"x\"},\n  ]\n}\n":               "line 5, column 3",
		"{\n  \"version\": 1,\n  \"defaults\": {\"agent\": \"codex\"}\n  \"agents\": []\n}\n": "line 4, column 3",
		"[{\"id\": \"x\"}]":            "should be one object",
		"{\n  \"version\": \"one\"\n}": `line 2, column 18: "version" cannot be string`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		notice := LoadFrom(dir).Notice
		if !strings.Contains(notice, want) || strings.Contains(notice, "agent.plain") {
			t.Errorf("notice %q, want it to say %q", notice, want)
		}
	}
}

// TestSetBaseURLGivesAnEndpointItsAddress: the OpenAI-compatible entry needs
// an address before it can be used, and it could be given only by editing
// agents.json by hand.
func TestSetBaseURLGivesAnEndpointItsAddress(t *testing.T) {
	dir := t.TempDir()
	if err := SetBaseURL(dir, "openai-compatible", "http://127.0.0.1:11434/v1"); err != nil {
		t.Fatal(err)
	}
	c := LoadFrom(dir)
	if s, _ := c.Find("openai-compatible"); s.API.BaseURL != "http://127.0.0.1:11434/v1" || !NeedsNoKey(s) || c.Notice != "" {
		t.Errorf("spec = %+v, notice %q; want the address recorded and usable", s.API, c.Notice)
	}

	// An entry that is already there keeps everything else it says.
	src := `{"agents": [{"id": "local", "name": "Local llama", "api": {"wire": "openai", "baseURL": "http://old/v1"}, "models": [{"id": "qwen3-coder"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetBaseURL(dir, "local", "http://127.0.0.1:1234/v1"); err != nil {
		t.Fatal(err)
	}
	s, _ := LoadFrom(dir).Find("local")
	if s.API.BaseURL != "http://127.0.0.1:1234/v1" || s.Name != "Local llama" || s.API.Wire != "openai" || len(s.Models) != 1 {
		t.Errorf("spec = %+v; want the address changed and the rest kept", s)
	}

	if err := SetBaseURL(dir, "local", "localhost:1234"); err == nil || !strings.Contains(err.Error(), "http://") {
		t.Errorf("an address with no scheme: %v", err)
	}
	if err := SetBaseURL(dir, "local", ""); err != nil {
		t.Fatal(err)
	}
	if s, _ := LoadFrom(dir).Find("local"); s.API.BaseURL != "" || s.Name != "Local llama" {
		t.Errorf("spec = %+v; want the address taken away and the rest kept", s)
	}
}
