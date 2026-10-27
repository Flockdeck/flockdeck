package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keys that no build reads any more must not make a file that carries them
// fail, change what it means, or put a notice up. Each case is a file as an
// older build, or somebody following older documentation, would have written it.

func TestRetiredSwitchKeyIsPassedOver(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"version":1,"agents":[
		{"id":"codex","defaultModel":"gpt-5.6-luna","switch":"/model {{model}}"},
		{"id":"mine","name":"Mine","runner":"cli","exe":"mine","switch":"/model {{modle}}"}
	]}`)
	c := LoadFrom(dir)
	if c.Notice != "" {
		t.Errorf("notice = %q, want none for a retired key", c.Notice)
	}
	spec, ok := c.Find("codex")
	if !ok || spec.DefaultModel != "gpt-5.6-luna" {
		t.Errorf("codex = %+v, want the entry's other fields to apply", spec)
	}
	if mine, ok := c.Find("mine"); !ok || mine.Exe != "mine" {
		t.Errorf("mine = %+v, want the entry to load", mine)
	}
}

func TestRetiredSwitchKeyIsKeptWhenTheFileIsSaved(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"version":1,"agents":[{"id":"codex","switch":"/model {{model}}"}]}`)
	f, err := ReadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.Defaults.Agent = "codex"
	if err := WriteConfig(dir, f); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"switch"`) {
		t.Errorf("saving a default dropped the entry's switch key: %s", data)
	}
}

func TestRetiredSuggestInPaneKeyIsPassedOver(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"version":1,"routing":{"mode":"auto","rules":[
		{"name":"big","tier":"top","suggestInPane":true,"when":{"minWords":50}}
	]},"projects":{"/p":{"routing":{"mode":"auto","rules":[
		{"name":"small","tier":"small","suggestInPane":false,"when":{"maxWords":3}}
	]}}}}`)
	c := LoadFrom(dir)
	if c.Notice != "" {
		t.Errorf("notice = %q, want none", c.Notice)
	}
	if len(c.Routing.Rules) != 1 || c.Routing.Rules[0].Name != "big" || c.Routing.Rules[0].Tier != "top" ||
		c.Routing.Rules[0].When.MinWords != 50 {
		t.Errorf("rules = %+v, want the rule to load as written", c.Routing.Rules)
	}
	if got := c.ProjectRouting["/p"].Rules; len(got) != 1 || got[0].Name != "small" {
		t.Errorf("project rules = %+v, want the project's rule to load", got)
	}

	// Saving a default writes the routing policy back as it was written.
	f, err := ReadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(dir, f); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ConfigName))
	if strings.Count(string(data), "suggestInPane") != 2 {
		t.Errorf("rewriting the file changed its routing: %s", data)
	}
}

// A file written by a later build may carry a version this one has not heard
// of. Nothing reads the number, so the file loads whatever it says.
func TestAnyVersionOfTheFileLoads(t *testing.T) {
	for _, version := range []string{"0", "1", "2", "99", `"3"`} {
		dir := t.TempDir()
		write(t, dir, `{"version":`+version+`,"defaults":{"agent":"codex"}}`)
		if version == `"3"` {
			// A string is not a number; this has always been a parse error
			// and is not something to start tolerating here.
			if c := LoadFrom(dir); c.Notice == "" {
				t.Errorf("version %s: want a notice for a version that is not a number", version)
			}
			continue
		}
		c := LoadFrom(dir)
		if c.Notice != "" || c.Defaults.Agent != "codex" {
			t.Errorf("version %s: notice = %q, default = %q", version, c.Notice, c.Defaults.Agent)
		}
	}
}

func TestFileWithoutAVersionLoads(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"defaults":{"agent":"codex"}}`)
	if c := LoadFrom(dir); c.Notice != "" || c.Defaults.Agent != "codex" {
		t.Errorf("notice = %q, default = %q", c.Notice, c.Defaults.Agent)
	}
}
