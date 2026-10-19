package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jmwri/flockdeck/internal/ghcli"
	"github.com/jmwri/flockdeck/internal/store"
)

// `flockdeck knowledge` is the switch for pointing a project's agents at its
// knowledge bundle (.flockdeck/knowledge). It writes the preferences file, which
// the briefing reads each time it is built, so it needs no running instance.
//
// `on` and `off` are refused inside a pane. That refusal is advisory: it reads
// the environment a pane is started with, which an agent can unset in the
// command it runs. The real barrier is the agent's own permission prompt for
// running a command. The refusal stops an agent doing it by accident or on a
// default, and says why; it does not stop one that is set on it.

func knowledgeUsage(w io.Writer) {
	fmt.Fprint(w, "Usage: flockdeck knowledge [status] [-project dir]\n"+
		"       flockdeck knowledge on [-project dir]\n"+
		"       flockdeck knowledge off [-project dir]\n\n"+
		"Whether the briefing of this project's agents points at its notes in\n"+
		".flockdeck/knowledge. The default is on for a repository gh reports as private\n"+
		"and off otherwise, with a one-time notice in the window. on and off must be run\n"+
		"outside a Flockdeck pane.\n")
}

// knowledgeEnv is what runKnowledge depends on outside the preferences, so a
// test can stand in for them.
type knowledgeEnv struct {
	inPane     func() bool
	projectOf  func(dir string) (string, error)
	visibility func(root string) (string, error)
}

func runKnowledge(args []string, out io.Writer) error {
	return runKnowledgeWith(knowledgeEnv{
		inPane:     func() bool { return quietPaneEnv("PANE") != "" },
		projectOf:  projectRootOf,
		visibility: ghcli.Visibility,
	}, args, out)
}

// knowledgeFlagSet is the flags of `flockdeck knowledge`.
func knowledgeFlagSet(project *string) *flag.FlagSet {
	fs := flag.NewFlagSet("knowledge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(project, "project", "", "project directory (default: the current directory)")
	return fs
}

func runKnowledgeWith(env knowledgeEnv, args []string, out io.Writer) error {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	var project string
	fs := knowledgeFlagSet(&project)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			knowledgeUsage(out)
			return nil
		}
		knowledgeUsage(os.Stderr)
		return errReported
	}
	if fs.NArg() > 0 {
		knowledgeUsage(os.Stderr)
		return errReported
	}
	switch action {
	case "help":
		knowledgeUsage(out)
		return nil
	case "status", store.KnowledgeOn, store.KnowledgeOff:
	default:
		knowledgeUsage(os.Stderr)
		return fmt.Errorf("unknown action %q", action)
	}
	if action != "status" && env.inPane() {
		return errors.New("this changes what is offered to agents, so it is not run from inside a pane; run it in a terminal outside Flockdeck")
	}
	dir := project
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = wd
	}
	root, err := env.projectOf(dir)
	if err != nil {
		return err
	}
	if action != "status" {
		// Read and written as one step, and refused if the file cannot be
		// read: saving defaults with one change over a file that exists would
		// lose every other setting.
		err := store.UpdatePrefs(func(p *store.Prefs) bool { return p.SetKnowledge(root, action) })
		if err != nil {
			return fmt.Errorf("could not save the setting: %w", err)
		}
		fmt.Fprintf(out, "Agents in %s: bundle pointer %s\n", root, action)
		return nil
	}
	p, err := store.ReadPrefs()
	if err != nil {
		return fmt.Errorf("the preferences could not be read: %w", err)
	}
	return knowledgeStatus(env, p, root, dir, out)
}

func knowledgeStatus(env knowledgeEnv, p store.Prefs, root, dir string, out io.Writer) error {
	// The root is what the window keys the setting by, so what is printed here
	// is the project the briefing will look up.
	fmt.Fprintf(out, "project: %s\n", root)
	if _, err := os.Stat(filepath.Join(dir, ".flockdeck", "knowledge", "index.md")); err != nil {
		fmt.Fprintln(out, "bundle: none in this directory")
	} else {
		fmt.Fprintln(out, "bundle: .flockdeck/knowledge/index.md")
	}
	if set := p.KnowledgeSetting(root); set != "" {
		fmt.Fprintf(out, "pointer: %s (set by you)\n", set)
		return nil
	}
	v, err := env.visibility(root)
	switch {
	case err != nil || v == "":
		fmt.Fprintln(out, "pointer: ask, off until answered (repository visibility is unknown)")
	case v == "private":
		fmt.Fprintln(out, "pointer: on (default, repository is private)")
	default:
		fmt.Fprintf(out, "pointer: ask, off until answered (repository is %s)\n", v)
	}
	return nil
}

// projectRootOf is the project dir belongs to, spelled as the window keys it:
// the folder it was opened as, when dir is in one that Flockdeck has open or
// has opened. That folder is not always a repository (it may hold several), so
// the repository's own root is only the answer for a directory no project
// contains, and is the main checkout of the repository for a worktree.
func projectRootOf(dir string) (string, error) {
	if root, ok := store.ProjectContaining(dir); ok {
		return root, nil
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s is not in a project Flockdeck has opened or in a git repository", dir)
	}
	common := filepath.Clean(strings.TrimSpace(string(b)))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	return common, nil
}
