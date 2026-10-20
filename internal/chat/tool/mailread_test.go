package tool

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// putApp stands in for the running application: an executable called
// flockdeck in a new directory, which is made the whole of PATH. It returns
// the path to it.
func putApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "flockdeck"
	if runtime.GOOS == "windows" {
		name = "flockdeck.exe"
	}
	exe := filepath.Join(dir, name)
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return exe
}

// The built-in client has no auto-approve switch, so the two forms of reading
// Flockdeck mail are run without asking, and everything near them is asked
// about as before. The matcher is review.MailRead's; this checks it is wired
// in with the same answers.
func TestRunCommandReadsFlockdeckMailWithoutAsking(t *testing.T) {
	root := newRoot(t)
	tl := &runCommand{root: root, self: putApp(t)}
	ask := func(cmd string) string {
		return tl.Approval(rawArgs(t, map[string]any{"command": cmd}))
	}

	for _, cmd := range []string{
		"flockdeck inbox", "flockdeck inbox -all -json", "flockdeck inbox read 12", "flockdeck inbox read -json 123456789",
	} {
		if q := ask(cmd); q != "" {
			t.Errorf("%q is asked about: %q", cmd, q)
		}
	}
	for _, cmd := range []string{
		"flockdeck inbox -wait 30s", "flockdeck inbox -follow", "flockdeck msg send x y", "flockdeck spawn x",
		"flockdeck inbox read 12 13", "flockdeck inbox read 1234567890", "flockdeck inbox read abc",
		"./flockdeck inbox", "FLOCKDECK_PANE=x flockdeck inbox", `flockdeck inbox read "12"`,
	} {
		if q := ask(cmd); q == "" {
			t.Errorf("%q is not asked about", cmd)
		}
	}
}

// A directory holding a program called flockdeck is asked about even for the
// exact command.
func TestRunCommandAsksForMailWhereAFlockdeckIsPlanted(t *testing.T) {
	root := newRoot(t)
	if err := os.WriteFile(filepath.Join(root.Dir(), "flockdeck.exe"), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	tl := &runCommand{root: root, self: putApp(t)}
	if q := tl.Approval(rawArgs(t, map[string]any{"command": "flockdeck inbox"})); q == "" {
		t.Error("flockdeck inbox is not asked about in a directory holding flockdeck.exe")
	}
}

// Chained commands are not run by this tool at all; the matcher still refuses
// them, so nothing here depends on that.
func TestReadsFlockdeckMailRefusesChains(t *testing.T) {
	tl := &runCommand{root: newRoot(t), self: putApp(t)}
	for _, cmd := range []string{"flockdeck inbox; rm x", "flockdeck inbox && id", "flockdeck inbox\nid", "flockdeck inbox | sh"} {
		if tl.readsFlockdeckMail(cmd, t.TempDir()) {
			t.Errorf("%q was read as a mail read", cmd)
		}
	}
}

// A program called flockdeck earlier on the PATH than the application is asked
// about, as is a PATH with no flockdeck on it; the application's own directory
// is not.
func TestRunCommandAsksForMailWhenFlockdeckOnThePathIsNotTheApp(t *testing.T) {
	root := newRoot(t)
	tl := &runCommand{root: root, self: putApp(t)}
	app := os.Getenv("PATH")
	ask := func() string { return tl.Approval(rawArgs(t, map[string]any{"command": "flockdeck inbox"})) }
	if q := ask(); q != "" {
		t.Fatalf("the application on the PATH is asked about: %q", q)
	}

	name := filepath.Base(tl.self)
	planted := t.TempDir()
	if err := os.WriteFile(filepath.Join(planted, name), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", planted+string(os.PathListSeparator)+app)
	if q := ask(); q == "" {
		t.Error("a planted flockdeck ahead of the application is not asked about")
	}
	t.Setenv("PATH", t.TempDir())
	if q := ask(); q == "" {
		t.Error("a PATH with no flockdeck on it is not asked about")
	}
}
