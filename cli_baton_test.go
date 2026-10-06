package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/hooks"
)

// -baton takes the word after it, so it is moved in front of the task with its
// value, wherever it was typed. Were it a flag that may or may not take a
// value, "self" would be the first word of the task half the time.
func TestSpawnBatonFlagOrdering(t *testing.T) {
	fs := spawnFlagSet(&spawnFlags{})
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"after the task", []string{"finish the refresh", "--baton", "self"}, []string{"--baton", "self", "--", "finish the refresh"}},
		{"before the task", []string{"--baton", "self", "finish", "the", "refresh"}, []string{"--baton", "self", "--", "finish", "the", "refresh"}},
		{"written with =", []string{"finish it", "--baton=self"}, []string{"--baton=self", "--", "finish it"}},
		{"between other flags", []string{"-split", "finish it", "--baton", "20261001-090000-0a1b2c", "-worktree", "fix-auth"},
			[]string{"-split", "--baton", "20261001-090000-0a1b2c", "-worktree", "fix-auth", "--", "finish it"}},
		{"nothing after it is left alone", []string{"finish it", "--baton"}, []string{"--baton"}},
	}
	for _, c := range cases {
		got := orderSpawnArgs(fs, c.args)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s: orderSpawnArgs(%q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

func TestParseSpawnReadsTheBatonReference(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(notes, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "20261001-090000-0a1b2c"
	cases := []struct {
		name string
		args []string
		want hooks.SpawnRequest
	}{
		{"self after the task", []string{"the refresh part", "--baton", "self"}, hooks.SpawnRequest{Task: "the refresh part", Baton: "self"}},
		{"a baton id", []string{"--baton", id, "carry on"}, hooks.SpawnRequest{Task: "carry on", Baton: id}},
		{"a pane id is sent as typed", []string{"--baton", "3f2c9a1d-aaaa-bbbb-cccc-dddddddddddd", "carry on"},
			hooks.SpawnRequest{Task: "carry on", Baton: "3f2c9a1d-aaaa-bbbb-cccc-dddddddddddd"}},
		{"a file that exists is sent as its absolute path", []string{"--baton", notes, "carry on"},
			hooks.SpawnRequest{Task: "carry on", Baton: "path:" + notes}},
		// A baton carries the work, so the task is optional with one.
		{"no task", []string{"--baton", "self"}, hooks.SpawnRequest{Baton: "self"}},
		{"with the other flags", []string{"fix it", "--worktree", "fix-auth", "--baton", "self", "--split"},
			hooks.SpawnRequest{Task: "fix it", Branch: "fix-auth", Split: true, Baton: "self"}},
	}
	for _, c := range cases {
		got, err := parseSpawn(c.args)
		if err != nil {
			t.Errorf("%s: parseSpawn(%q) = %v", c.name, c.args, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: parseSpawn(%q) = %+v, want %+v", c.name, c.args, got, c.want)
		}
	}
	if _, err := parseSpawn([]string{"fix it", "-shell", "-baton", "self"}); err == nil {
		t.Error("a shell was asked to take a baton")
	}
	if _, err := parseSpawn([]string{"fix it", "--baton"}); err == nil {
		t.Error("-baton with no value was accepted")
	}
	if _, err := parseSpawn(nil); err == nil {
		t.Error("no task and no baton was accepted")
	}
}

func TestBatonShowAndList(t *testing.T) {
	s := baton.NewStore(filepath.Join(t.TempDir(), "batons"))
	var out bytes.Buffer
	if err := runBatonIn(s, []string{"list"}, &out); err != nil || !strings.Contains(out.String(), "no batons") {
		t.Fatalf("empty list = %q, %v", out.String(), err)
	}

	b := baton.Baton{
		ID: baton.NewID(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)), Title: "Add a retry", FromAgent: "claude",
		Created:  time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
		Sections: map[baton.Section]string{baton.Goal: "Add a retry to the fetch client."},
	}
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runBatonIn(s, []string{"list"}, &out); err != nil || !strings.Contains(out.String(), b.ID) || !strings.Contains(out.String(), "Add a retry") {
		t.Errorf("list = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := runBatonIn(s, []string{"show", b.ID}, &out); err != nil || !strings.Contains(out.String(), "## Goal") || !strings.Contains(out.String(), "Add a retry to the fetch client.") {
		t.Errorf("show = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := runBatonIn(s, []string{"show", "-path", b.ID}, &out); err != nil || strings.TrimSpace(out.String()) != s.Path(b.ID) {
		t.Errorf("show -path = %q, %v", out.String(), err)
	}
	// A name that is not an id is never joined to the folder.
	for _, bad := range []string{"20991231-000000-ffffff", `..\..\x`, "../x"} {
		if err := runBatonIn(s, []string{"show", bad}, &out); err == nil || !strings.Contains(err.Error(), "no baton") {
			t.Errorf("show %q = %v", bad, err)
		}
	}
	if err := runBatonIn(s, []string{"frobnicate"}, &out); err == nil {
		t.Error("an unknown subcommand was accepted")
	}
}

func TestBatonSendElsewhereNeedsABaton(t *testing.T) {
	got, err := parseSpawn([]string{"fix it", "--baton", "self", "--baton-send-elsewhere"})
	if err != nil || !got.BatonElsewhere || got.Baton != "self" {
		t.Errorf("parseSpawn = %+v, %v", got, err)
	}
	if _, err := parseSpawn([]string{"fix it", "--baton-send-elsewhere"}); err == nil {
		t.Error("-baton-send-elsewhere was accepted with no -baton")
	}
	// It is a switch, so the word after it stays part of the task.
	fs := spawnFlagSet(&spawnFlags{})
	got2 := orderSpawnArgs(fs, []string{"--baton", "self", "--baton-send-elsewhere", "fix", "it"})
	if strings.Join(got2, " ") != "--baton self --baton-send-elsewhere -- fix it" {
		t.Errorf("orderSpawnArgs = %q", got2)
	}
}

// A file in the baton folder is anyone's to write, and show and list print to a
// terminal.
func TestBatonShowAndListStripTerminalEscapes(t *testing.T) {
	s := baton.NewStore(t.TempDir())
	id := baton.NewID(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	text := "---\nid: " + id + "\nagent: cl\x1b[31maude\n---\n\n# Baton: evil\x1b]0;pwned\x07 title\n\n## Goal\n\nhide\x1b[2J this\n"
	if err := os.WriteFile(s.Path(id), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"list"}, {"show", id}} {
		var out bytes.Buffer
		if err := runBatonIn(s, args, &out); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(out.String(), "\x1b\x07") || !strings.Contains(out.String(), "evil title") {
			t.Errorf("%v printed %q", args, out.String())
		}
	}
}

// Showing a baton is a use of it: it does not age out under somebody who is
// reading it.
func TestShowingABatonRecordsTheUse(t *testing.T) {
	s := baton.NewStore(filepath.Join(t.TempDir(), "batons"))
	b := baton.Baton{
		ID: baton.NewID(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)), Title: "Old",
		Sections: map[baton.Section]string{baton.Goal: "finish"},
	}
	if err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(s.Path(b.ID), long, long); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBatonIn(s, []string{"show", b.ID}, &out); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.Path(b.ID))
	if err != nil || time.Since(fi.ModTime()) > time.Hour {
		t.Errorf("showing the baton did not record its use: %v %v", fi, err)
	}
}
