package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bashInput(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMailReadAllowsTheExactForms(t *testing.T) {
	dir := t.TempDir()
	for _, cmd := range []string{
		"flockdeck inbox",
		"flockdeck inbox -all",
		"flockdeck inbox -json",
		"flockdeck inbox -all -json",
		"flockdeck inbox -json -all",
		"flockdeck inbox read 1",
		"flockdeck inbox read 12",
		"flockdeck inbox read 123456789",
		"flockdeck inbox read 000000001",
		"flockdeck inbox read -json 7",
		"flockdeck inbox read 7 -json",
	} {
		d := MailRead("Bash", bashInput(t, cmd), dir)
		if !d.Allow || d.Reason != "allow: reading Flockdeck mail" {
			t.Errorf("%q: got %+v, want it allowed with the mail reason", cmd, d)
		}
	}
}

func TestMailReadRefuses(t *testing.T) {
	dir := t.TempDir()
	long := "flockdeck inbox " + strings.Repeat("-all ", 1000)
	for name, cmd := range map[string]string{
		"chained":              "flockdeck inbox read 12; rm x",
		"and":                  "flockdeck inbox && id",
		"pipe":                 "flockdeck inbox | sh",
		"substitution":         "flockdeck inbox read $(id)",
		"backtick":             "flockdeck inbox read `id`",
		"redirect":             "flockdeck inbox > x",
		"newline":              "flockdeck inbox\nid",
		"carriage return":      "flockdeck inbox\rid",
		"tab separated":        "flockdeck\tinbox",
		"vertical tab":         "flockdeck inbox\vread 1",
		"form feed":            "flockdeck inbox\f-all",
		"nbsp separated":       "flockdeck inbox",
		"next line separated":  "flockdeck\u0085inbox",
		"ideographic space":    "flockdeck inbox　-all",
		"nul":                  "flockdeck inbox\x00",
		"escape":               "flockdeck inbox\x1b",
		"non-numeric id":       "flockdeck inbox read abc",
		"two ids":              "flockdeck inbox read 12 13",
		"ten digit id":         "flockdeck inbox read 1234567890",
		"negative id":          "flockdeck inbox read -1",
		"signed id":            "flockdeck inbox read +1",
		"hex id":               "flockdeck inbox read 0x1",
		"unicode digit id":     "flockdeck inbox read ١",
		"no id":                "flockdeck inbox read",
		"no id with json":      "flockdeck inbox read -json",
		"id for the list form": "flockdeck inbox 12",
		"follow":               "flockdeck inbox -follow",
		"wait":                 "flockdeck inbox -wait 30s",
		"wait equals":          "flockdeck inbox -wait=30s",
		"double dash":          "flockdeck inbox --json",
		"json with value":      "flockdeck inbox -json=true",
		"all twice":            "flockdeck inbox -all -all",
		"json twice":           "flockdeck inbox -json -json",
		"json twice in read":   "flockdeck inbox read -json -json 1",
		"all in read":          "flockdeck inbox read -all 1",
		"extra word":           "flockdeck inbox extra",
		"extra after read id":  "flockdeck inbox read 1 extra",
		"list then read":       "flockdeck inbox -all read 1",
		"msg send":             "flockdeck msg send x y",
		"spawn":                "flockdeck spawn x",
		"close":                "flockdeck close",
		"task":                 "flockdeck task list",
		"team":                 "flockdeck team",
		"inbox as a prefix":    "flockdeck inboxes",
		"bare program":         "flockdeck",
		"relative path":        "./flockdeck inbox",
		"absolute path":        "/usr/bin/flockdeck inbox",
		"exe":                  "flockdeck.exe inbox",
		"env var prefix":       "FLOCKDECK_PANE=x flockdeck inbox",
		"env command":          "env flockdeck inbox",
		"upper case":           "Flockdeck inbox",
		"quoted argument":      `flockdeck inbox read "12"`,
		"single quoted":        "flockdeck inbox read '12'",
		"quoted program":       `"flockdeck" inbox`,
		"backslash":            `flockdeck inbox read 1\2`,
		"glob":                 "flockdeck inbox *",
		"brace":                "flockdeck inbox {a,b}",
		"leading space":        " flockdeck inbox",
		"trailing space":       "flockdeck inbox ",
		"double space":         "flockdeck  inbox",
		"comment":              "flockdeck inbox #",
		"overlong":             long,
		"empty":                "",
	} {
		if d := MailRead("Bash", bashInput(t, cmd), dir); d.Allow || d.Reason != "" {
			t.Errorf("%s (%q): got %+v, want it refused", name, cmd, d)
		}
	}
}

func TestMailReadRefusesOtherToolsAndInputs(t *testing.T) {
	dir := t.TempDir()
	good := bashInput(t, "flockdeck inbox")
	for name, c := range map[string]struct{ tool, input string }{
		"Read tool":      {"Read", good},
		"lower case":     {"bash", good},
		"empty tool":     {"", good},
		"empty input":    {"Bash", ""},
		"not json":       {"Bash", "flockdeck inbox"},
		"no command":     {"Bash", `{}`},
		"command number": {"Bash", `{"command":1}`},
		"command list":   {"Bash", `{"command":["flockdeck","inbox"]}`},
		"command null":   {"Bash", `{"command":null}`},
	} {
		if d := MailRead(c.tool, c.input, dir); d.Allow {
			t.Errorf("%s: allowed", name)
		}
	}
}

func TestMailReadChecksTheDirectory(t *testing.T) {
	in := bashInput(t, "flockdeck inbox")
	if d := MailRead("Bash", in, ""); d.Allow {
		t.Error("an empty cwd was allowed")
	}
	if d := MailRead("Bash", in, "relative/dir"); d.Allow {
		t.Error("a relative cwd was allowed")
	}
	dir := t.TempDir()
	if d := MailRead("Bash", in, filepath.Join(dir, "missing")); d.Allow {
		t.Error("a cwd that does not exist was allowed")
	}
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, file); d.Allow {
		t.Error("a cwd that is a file was allowed")
	}
	if d := MailRead("Bash", in, dir); !d.Allow {
		t.Error("an empty directory was refused")
	}

	// A planted program of that name, of any kind a shell would run, in the
	// directory the command runs in.
	for _, name := range plantable {
		planted := t.TempDir()
		if err := os.WriteFile(filepath.Join(planted, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if d := MailRead("Bash", in, planted); d.Allow {
			t.Errorf("a directory holding %s was allowed", name)
		}
	}
	// A directory of that name is refused too.
	sub := t.TempDir()
	if err := os.Mkdir(filepath.Join(sub, "flockdeck"), 0o700); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, sub); d.Allow {
		t.Error("a directory holding a folder called flockdeck was allowed")
	}
}

// MailRead does not widen Decide: the mail forms are not read-only commands,
// and Decide's own answers are unchanged by its existence.
func TestMailReadIsSeparateFromDecide(t *testing.T) {
	dir := t.TempDir()
	for _, cmd := range []string{"flockdeck inbox", "flockdeck inbox read 1"} {
		if d := Decide("Bash", bashInput(t, cmd), dir); d.Allow {
			t.Errorf("Decide allowed %q", cmd)
		}
	}
	if d := MailRead("Bash", bashInput(t, "cat file"), dir); d.Allow {
		t.Error("MailRead allowed cat")
	}
}
