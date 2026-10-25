package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every flag of `flockdeck spawn` is in the usage line the help page opens with and
// in the table of flags agents are given, so a flag added to the command cannot go
// undocumented, and -no-worktree is named where a helper's worktree is explained.
func TestEverySpawnFlagIsDocumented(t *testing.T) {
	var f spawnFlags
	fs := spawnFlagSet(&f)
	help, err := os.ReadFile(filepath.Join("ui", "help", "pages", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := os.ReadFile(filepath.Join(".flockdeck", "knowledge", "cli", "spawn.md"))
	if err != nil {
		t.Fatal(err)
	}
	fs.VisitAll(func(fl *flag.Flag) {
		if !strings.Contains(string(help), "-"+fl.Name) {
			t.Errorf("-%s is not in ui/help/pages/cli.md", fl.Name)
		}
		if !strings.Contains(string(knowledge), "`-"+fl.Name) {
			t.Errorf("-%s is not in .flockdeck/knowledge/cli/spawn.md", fl.Name)
		}
	})
	agents, err := os.ReadFile(filepath.Join("ui", "help", "pages", "agents.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "-no-worktree") {
		t.Error("ui/help/pages/agents.md does not name -no-worktree")
	}
}

func TestWorktreeAndNoWorktreeCannotGoTogether(t *testing.T) {
	if _, err := parseSpawn([]string{"-worktree", "b", "-no-worktree", "task"}); err == nil {
		t.Error("-worktree with -no-worktree was accepted")
	}
	req, err := parseSpawn([]string{"-no-worktree", "task"})
	if err != nil || !req.NoWorktree {
		t.Errorf("req = %+v, %v", req, err)
	}
}
