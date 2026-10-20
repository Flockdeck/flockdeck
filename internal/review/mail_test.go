package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	env := runningEnv(t)
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
	} {
		d := MailRead("Bash", bashInput(t, cmd), dir, env)
		if !d.Allow || d.Reason != "allow: reading Flockdeck mail" {
			t.Errorf("%q: got %+v, want it allowed with the mail reason", cmd, d)
		}
	}
}

func TestMailReadRefuses(t *testing.T) {
	env := runningEnv(t)
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
		"json after the id":    "flockdeck inbox read 7 -json",
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
		if d := MailRead("Bash", bashInput(t, cmd), dir, env); d.Allow || d.Reason != "" {
			t.Errorf("%s (%q): got %+v, want it refused", name, cmd, d)
		}
	}
}

func TestMailReadRefusesOtherToolsAndInputs(t *testing.T) {
	env := runningEnv(t)
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
		if d := MailRead(c.tool, c.input, dir, env); d.Allow {
			t.Errorf("%s: allowed", name)
		}
	}
}

func TestMailReadChecksTheDirectory(t *testing.T) {
	env := runningEnv(t)
	in := bashInput(t, "flockdeck inbox")
	if d := MailRead("Bash", in, "", env); d.Allow {
		t.Error("an empty cwd was allowed")
	}
	if d := MailRead("Bash", in, "relative/dir", env); d.Allow {
		t.Error("a relative cwd was allowed")
	}
	dir := t.TempDir()
	if d := MailRead("Bash", in, filepath.Join(dir, "missing"), env); d.Allow {
		t.Error("a cwd that does not exist was allowed")
	}
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, file, env); d.Allow {
		t.Error("a cwd that is a file was allowed")
	}
	if d := MailRead("Bash", in, dir, env); !d.Allow {
		t.Error("an empty directory was refused")
	}

	// A planted program of that name, of any kind a shell would run, in the
	// directory the command runs in.
	for _, name := range []string{"flockdeck", "flockdeck.exe", "flockdeck.cmd", "flockdeck.bat", "flockdeck.com", "flockdeck.ps1", "flockdeck.vbs", "flockdeck.js", "flockdeck.wsf", "flockdeck.msc", "flockdeck.sh", "flockdeck.", "FLOCKDECK", "FlockDeck.EXE", "flockdeck.tar.gz"} {
		planted := t.TempDir()
		if err := os.WriteFile(filepath.Join(planted, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if d := MailRead("Bash", in, planted, env); d.Allow {
			t.Errorf("a directory holding %s was allowed", name)
		}
	}
	// A directory of that name is refused too.
	sub := t.TempDir()
	if err := os.Mkdir(filepath.Join(sub, "flockdeck"), 0o700); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, sub, env); d.Allow {
		t.Error("a directory holding a folder called flockdeck was allowed")
	}
}

// MailRead does not widen Decide: the mail forms are not read-only commands,
// and Decide's own answers are unchanged by its existence.
func TestMailReadIsSeparateFromDecide(t *testing.T) {
	env := runningEnv(t)
	dir := t.TempDir()
	for _, cmd := range []string{"flockdeck inbox", "flockdeck inbox read 1"} {
		if d := Decide("Bash", bashInput(t, cmd), dir); d.Allow {
			t.Errorf("Decide allowed %q", cmd)
		}
	}
	if d := MailRead("Bash", bashInput(t, "cat file"), dir, env); d.Allow {
		t.Error("MailRead allowed cat")
	}
}

// Names that only look like the program are not planted programs.
func TestMailReadAllowsDirectoriesWithOnlyLookalikes(t *testing.T) {
	env := runningEnv(t)
	in := bashInput(t, "flockdeck inbox")
	dir := t.TempDir()
	for _, name := range []string{"flockdecks", "my-flockdeck.exe", "flockdeck-notes.md", "notflockdeck", "flock.deck"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if d := MailRead("Bash", in, dir, env); !d.Allow {
		t.Error("a directory with only lookalike names was refused")
	}
}

// selfName is the file name the running application has on this platform.
func selfName() string {
	if runtime.GOOS == "windows" {
		return "flockdeck.exe"
	}
	return "flockdeck"
}

// plant writes an executable file called name in a new directory and returns
// the directory.
func plant(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runningEnv is an environment in which the application is on the PATH: the
// word flockdeck resolves to Self.
func runningEnv(t *testing.T) MailEnv {
	t.Helper()
	dir := plant(t, selfName())
	return MailEnv{Path: dir, Self: filepath.Join(dir, selfName())}
}

func TestMailReadResolvesFlockdeckOnThePath(t *testing.T) {
	in := bashInput(t, "flockdeck inbox")
	cwd := t.TempDir()
	self := runningEnv(t)
	sep := string(os.PathListSeparator)
	planted := plant(t, selfName())

	cases := []struct {
		name  string
		path  string
		self  string
		allow bool
	}{
		{"the application", self.Path, self.Self, true},
		{"the application after unrelated entries", t.TempDir() + sep + self.Path, self.Self, true},
		{"a planted program first", planted + sep + self.Path, self.Self, false},
		{"a planted program only", planted, self.Self, false},
		{"nothing called flockdeck", t.TempDir(), self.Self, false},
		{"an empty PATH", "", self.Self, false},
		{"no application known", self.Path, "", false},
		{"a planted program after the application", self.Path + sep + planted, self.Self, true},
		{"a missing directory first", filepath.Join(cwd, "nope") + sep + self.Path, self.Self, true},
	}
	for _, c := range cases {
		d := MailRead("Bash", in, cwd, MailEnv{Path: c.path, Self: c.self})
		if d.Allow != c.allow {
			t.Errorf("%s: allow = %v, want %v", c.name, d.Allow, c.allow)
		}
	}
}

// A file that is not the application but sits in its directory counts: the
// application is found by its directory, as spawnCommand finds it.
func TestMailReadAllowsTheApplicationsDirectory(t *testing.T) {
	in := bashInput(t, "flockdeck inbox")
	dir := plant(t, selfName())
	other := filepath.Join(t.TempDir(), selfName())
	if err := os.WriteFile(other, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: dir, Self: other}); d.Allow {
		t.Error("a copy in another directory was allowed")
	}
	// Self named by a different file in the same directory.
	sibling := filepath.Join(dir, "flockdeck-desktop.exe")
	if err := os.WriteFile(sibling, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: dir, Self: sibling}); !d.Allow {
		t.Error("a program in the application's directory was refused")
	}
}

// An empty or relative PATH entry is the directory the command runs in.
func TestMailReadTakesRelativePathEntriesFromTheDirectory(t *testing.T) {
	in := bashInput(t, "flockdeck inbox")
	self := runningEnv(t)
	sep := string(os.PathListSeparator)
	// A cwd holding a planted program is already refused; one holding it in a
	// subdirectory is reached only through a relative entry.
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "bin", selfName()), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"bin" + sep + self.Path, "./bin" + sep + self.Path} {
		if d := MailRead("Bash", in, cwd, MailEnv{Path: path, Self: self.Self}); d.Allow {
			t.Errorf("PATH %q: a program under a relative entry was allowed", path)
		}
	}
	// An empty entry is the cwd, which holds nothing here, so the application
	// after it is found.
	if d := MailRead("Bash", in, cwd, MailEnv{Path: sep + self.Path, Self: self.Self}); !d.Allow {
		t.Error("an empty entry with nothing in the directory was refused")
	}
}

func TestMailReadFollowsSymbolicLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	in := bashInput(t, "flockdeck inbox")
	self := runningEnv(t)
	link := t.TempDir()
	if err := os.Symlink(self.Self, filepath.Join(link, "flockdeck")); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: link, Self: self.Self}); !d.Allow {
		t.Error("a link to the application was refused")
	}
	other := plant(t, "flockdeck")
	bad := t.TempDir()
	if err := os.Symlink(filepath.Join(other, "flockdeck"), filepath.Join(bad, "flockdeck")); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: bad + string(os.PathListSeparator) + self.Path, Self: self.Self}); d.Allow {
		t.Error("a link to another program was allowed")
	}
}

func TestMailReadIgnoresFilesThatCannotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("any file with the extension runs on Windows")
	}
	in := bashInput(t, "flockdeck inbox")
	self := runningEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flockdeck"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := dir + string(os.PathListSeparator) + self.Path
	if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: path, Self: self.Self}); !d.Allow {
		t.Error("a file without the execute bit shadowed the application")
	}
}

// A symbolic link called flockdeck in the application's own directory that
// points at a program elsewhere is that program, not the application.
func TestMailReadResolvesALinkInTheApplicationsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	in := bashInput(t, "flockdeck inbox")
	appDir := plant(t, "flockdeck-real")
	self := filepath.Join(appDir, "flockdeck-real")
	elsewhere := plant(t, "evil")
	if err := os.Symlink(filepath.Join(elsewhere, "evil"), filepath.Join(appDir, "flockdeck")); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: appDir, Self: self}); d.Allow {
		t.Error("a link in the application's directory to a program elsewhere was allowed")
	}
}

// On Windows an extensionless file called flockdeck, which Git Bash runs, is
// found ahead of the application in a later directory.
func TestMailReadFindsAnExtensionlessProgramOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("extensions are a Windows matter")
	}
	in := bashInput(t, "flockdeck inbox")
	self := runningEnv(t)
	shim := plant(t, "flockdeck")
	path := shim + string(os.PathListSeparator) + self.Path
	if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: path, Self: self.Self}); d.Allow {
		t.Error("an extensionless flockdeck ahead of the application was allowed")
	}
}

func TestCandidatesStartWithTheBareNameOnWindows(t *testing.T) {
	got := candidates("flockdeck")
	if got[0] != "flockdeck" {
		t.Errorf("first candidate = %q, want the bare name", got[0])
	}
	if runtime.GOOS != "windows" {
		if len(got) != 1 {
			t.Errorf("candidates = %v, want just the name", got)
		}
		return
	}
	t.Setenv("PATHEXT", ".EXE;.CMD")
	got = candidates("flockdeck")
	want := []string{"flockdeck", "flockdeck.EXE", "flockdeck.CMD"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("candidates = %v, want %v", got, want)
	}
}

func TestMailReadAsksWhenThePathHasATilde(t *testing.T) {
	in := bashInput(t, "flockdeck inbox")
	self := runningEnv(t)
	sep := string(os.PathListSeparator)
	for _, path := range []string{"~/bin" + sep + self.Path, "~" + sep + self.Path, self.Path + sep + "~/bin"} {
		if d := MailRead("Bash", in, t.TempDir(), MailEnv{Path: path, Self: self.Self}); d.Allow {
			t.Errorf("PATH %q was allowed", path)
		}
	}
}

func TestMailEnvFrom(t *testing.T) {
	got := MailEnvFrom([]string{"A=1", "PATH=/first", "novalue", "PATH=/last"}, "/app")
	if got.Path != "/last" || got.Self != "/app" {
		t.Errorf("got %+v, want the last PATH and the application", got)
	}
	lower := MailEnvFrom([]string{"Path=/win"}, "")
	if runtime.GOOS == "windows" && lower.Path != "/win" {
		t.Errorf("Path was not matched on Windows: %+v", lower)
	}
	if runtime.GOOS != "windows" && lower.Path != "" {
		t.Errorf("Path was matched as PATH off Windows: %+v", lower)
	}
}

func TestSameDirIgnoresCaseWhereVolumesDo(t *testing.T) {
	got := sameDir(filepath.Join(string(filepath.Separator), "Tmp", "App"), filepath.Join(string(filepath.Separator), "tmp", "app"))
	want := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	if got != want {
		t.Errorf("sameDir ignoring case = %v, want %v", got, want)
	}
}

func TestMailReadIgnoresTrailingDotsAndSpacesInNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not keep a trailing dot or space in a name")
	}
	in := bashInput(t, "flockdeck inbox")
	env := runningEnv(t)
	for _, name := range []string{"flockdeck ", "flockdeck. ", "flockdeck.exe.", "FlockDeck  "} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if d := MailRead("Bash", in, dir, env); d.Allow {
			t.Errorf("a directory holding %q was allowed", name)
		}
	}
}

func TestNamesFlockdeck(t *testing.T) {
	for name, want := range map[string]bool{
		"flockdeck": true, "FLOCKDECK": true, "flockdeck.exe": true, "flockdeck ": true, "flockdeck.": true,
		"flockdeck. .": true, "flockdecks": false, "xflockdeck": false, "flock deck": false, "": false,
	} {
		if got := namesFlockdeck(name); got != want {
			t.Errorf("namesFlockdeck(%q) = %v, want %v", name, got, want)
		}
	}
}

// A directory is read past the first batch, and a hit after it is found.
func TestMailReadFindsAPlantedFileAfterTheFirstBatch(t *testing.T) {
	in := bashInput(t, "flockdeck inbox")
	env := runningEnv(t)
	dir := t.TempDir()
	for i := 0; i < mailDirBatch*2+10; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%04d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if d := MailRead("Bash", in, dir, env); !d.Allow {
		t.Error("a directory of many unrelated files was refused")
	}
	if err := os.WriteFile(filepath.Join(dir, "zzz-flockdeck.exe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, dir, env); !d.Allow {
		t.Error("a lookalike name was refused")
	}
	if err := os.WriteFile(filepath.Join(dir, "flockdeck.exe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, dir, env); d.Allow {
		t.Error("a planted file in a large directory was allowed")
	}
}

// A directory above the bound is refused whatever it holds.
func TestMailReadRefusesAnOverlargeDirectory(t *testing.T) {
	old := mailDirMax
	mailDirMax = 20
	t.Cleanup(func() { mailDirMax = old })
	in := bashInput(t, "flockdeck inbox")
	env := runningEnv(t)
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if d := MailRead("Bash", in, dir, env); !d.Allow {
		t.Error("a directory at the bound was refused")
	}
	if err := os.WriteFile(filepath.Join(dir, "one-more"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if d := MailRead("Bash", in, dir, env); d.Allow {
		t.Error("a directory over the bound was allowed")
	}
}

// A directory that cannot be listed is refused. The cwd is a file here, which
// the earlier stat check catches; a read error in the loop is a path no test
// can reach without a file system that fails, so this pins the stat.
func TestMailReadRefusesADirectoryThatCannotBeRead(t *testing.T) {
	env := runningEnv(t)
	in := bashInput(t, "flockdeck inbox")
	if d := MailRead("Bash", in, filepath.Join(t.TempDir(), "gone"), env); d.Allow {
		t.Error("a directory that cannot be read was allowed")
	}
}
