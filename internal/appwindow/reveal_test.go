package appwindow

import (
	"errors"
	"strings"
	"testing"
)

func TestRevealPlanPerPlatform(t *testing.T) {
	cases := []struct {
		goos, path string
		want       []string // name and args of each step, joined by spaces
	}{
		{"windows", `C:\Users\me\My Files\t.jsonl`, []string{`explorer.exe [raw: explorer.exe /select,"C:\Users\me\My Files\t.jsonl"]`}},
		{"darwin", "/Users/me/My Files/t.jsonl", []string{"/usr/bin/open -R /Users/me/My Files/t.jsonl"}},
		{"linux", "/home/me/a,b c/t.jsonl", []string{
			"dbus-send --session --print-reply --dest=org.freedesktop.FileManager1 --type=method_call /org/freedesktop/FileManager1 org.freedesktop.FileManager1.ShowItems array:string:file:///home/me/a%2Cb%20c/t.jsonl string:",
			"xdg-open /home/me/a,b c"}},
	}
	for _, c := range cases {
		steps, err := revealPlan(c.goos, c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.goos, err)
		}
		var got []string
		for _, s := range steps {
			line := s.name + " " + strings.Join(s.args, " ")
			if s.rawLine != "" {
				line = s.name + " [raw: " + s.rawLine + "]"
			}
			got = append(got, strings.TrimSpace(line))
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\n%s\nwant\n%s", c.goos, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// A path is a path, never a flag, and never anything a hand-built command line
// would read differently.
func TestRevealRefusesWhatIsNotAnAbsolutePath(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		for _, path := range []string{"", "t.jsonl", "-R", "--help", "../t.jsonl", "/ok/\nx", "/ok/\x00x", "-/x"} {
			if _, err := revealPlan(goos, path); err == nil {
				t.Errorf("%s: %q was accepted", goos, path)
			}
		}
	}
	if _, err := revealPlan("windows", `C:\a"b\t.jsonl`); err == nil {
		t.Error(`a quote in a Windows path was accepted`)
	}
}

func TestRevealTriesTheFallbackOnlyWhenTheFirstFails(t *testing.T) {
	var ran []string
	run := func(fail string) revealRunner {
		return func(s revealStep) error {
			ran = append(ran, s.name)
			if s.name == fail {
				return errors.New("no")
			}
			return nil
		}
	}
	if err := reveal("linux", "/h/t.jsonl", run("")); err != nil || len(ran) != 1 || ran[0] != "dbus-send" {
		t.Errorf("ran %v, %v", ran, err)
	}
	ran = nil
	if err := reveal("linux", "/h/t.jsonl", run("dbus-send")); err != nil || len(ran) != 2 || ran[1] != "xdg-open" {
		t.Errorf("ran %v, %v", ran, err)
	}
	ran = nil
	failing := func(revealStep) error { return errors.New("no") }
	if err := reveal("linux", "/h/t.jsonl", failing); err == nil {
		t.Error("both failing was not an error")
	}
	if err := reveal("linux", "-x", func(revealStep) error { t.Error("ran something for a bad path"); return nil }); err == nil {
		t.Error("a bad path was accepted")
	}
}

// What is an absolute path is the platform's rule, not the machine the test runs
// on's: the same answers on every host.
func TestAbsoluteIsDecidedByThePlatformNotTheHost(t *testing.T) {
	for _, c := range []struct {
		goos, path string
		want       bool
	}{
		{"windows", `C:\Users\me\t.jsonl`, true},
		{"windows", `c:/Users/me/t.jsonl`, true},
		{"windows", `\\server\share\t.jsonl`, true},
		{"windows", `/Users/me/t.jsonl`, false},
		{"windows", `C:t.jsonl`, false},
		{"windows", `t.jsonl`, false},
		{"linux", "/home/me/t.jsonl", true},
		{"linux", `C:\Users\me\t.jsonl`, false},
		{"darwin", "/Users/me/t.jsonl", true},
		{"darwin", "t.jsonl", false},
	} {
		if got := absoluteOn(c.goos, c.path); got != c.want {
			t.Errorf("absoluteOn(%s, %q) = %v", c.goos, c.path, got)
		}
	}
}
