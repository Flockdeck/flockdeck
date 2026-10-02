package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// exportEnv is what `flockdeck recordings export` reads, so that a test can
// hand it a state directory, a catalog and saved layouts of its own.
type exportEnv struct {
	stateDir func() (string, error)
	// catalog is the agents Flockdeck knows, and the id of the default one.
	catalog func() ([]agent.Spec, string)
	// pane finds a pane in the saved layouts by its id.
	pane func(id string) (savedPane, bool)
}

// savedPane is a pane as the saved layouts hold it.
type savedPane struct {
	store.Pane
	// Root is the project the layout belongs to.
	Root string
}

func realExportEnv() exportEnv {
	return exportEnv{
		stateDir: store.Dir,
		catalog: func() ([]agent.Spec, string) {
			specs, def, _ := agentCatalog()
			return specs, def
		},
		pane: findSavedPane,
	}
}

// findSavedPane looks for a pane in the saved layout of every project Flockdeck
// knows. It reads them and changes nothing.
func findSavedPane(id string) (savedPane, bool) {
	projects, err := store.Recents()
	if err != nil {
		return savedPane{}, false
	}
	for _, p := range projects {
		st, err := store.Peek(p.Root)
		if err != nil || st == nil {
			continue
		}
		for _, tab := range st.Tabs {
			if sp, ok := paneIn(tab.Root, id); ok {
				return savedPane{Pane: sp, Root: p.Root}, true
			}
		}
	}
	return savedPane{}, false
}

func paneIn(n *store.Node, id string) (store.Pane, bool) {
	if n == nil {
		return store.Pane{}, false
	}
	if n.Pane != nil && n.Pane.ID == id {
		return *n.Pane, true
	}
	for _, c := range n.Children {
		if p, ok := paneIn(c, id); ok {
			return p, true
		}
	}
	return store.Pane{}, false
}

// exportFlags are the flags of `flockdeck recordings export`.
type exportFlags struct {
	path string
}

func exportFlagSet(f *exportFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("recordings export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&f.path, "o", "", "write the transcript to this `file`, which must not exist and must not be inside the project or a git repository; by default it goes in the exports folder under the recordings folder")
	return fs
}

// exportRecording implements `flockdeck recordings export`: it writes the
// conversation an agent has stored as a transcript in the recording format. It
// works with no instance running, and for a pane that was never recorded.
func exportRecording(args []string, out io.Writer, env exportEnv) error {
	var f exportFlags
	fs := exportFlagSet(&f)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: flockdeck recordings export [-o file] <pane-id | conversation-id>\n\n")
		fmt.Fprintf(os.Stderr, "Writes the whole conversation an agent has stored as a transcript, in the format\n")
		fmt.Fprintf(os.Stderr, "recordings use, whether or not the pane was ever recorded. A transcript is made\n")
		fmt.Fprintf(os.Stderr, "from the agent's own stored conversation, so a recording of it and an export of\n")
		fmt.Fprintf(os.Stderr, "it are the same lines. Claude Code is the agent whose conversations Flockdeck can\n")
		fmt.Fprintf(os.Stderr, "read; for any other, nothing is exported and nothing is written.\n\n")
		fmt.Fprintf(os.Stderr, "The id is a pane's, as the saved layouts hold it, or a Claude Code conversation's.\n")
		fmt.Fprintf(os.Stderr, "The transcript can contain secrets: common ones are removed and very long output\n")
		fmt.Fprintf(os.Stderr, "is clipped, but that is best effort. It is written 0600, never into a project.\n")
		fmt.Fprintf(os.Stderr, "The format is documented in docs/recording-format.md in the Flockdeck repository.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errReported
	}
	rest := fs.Args()
	if len(rest) > 1 {
		// Flags after the id: `export <id> -o file`.
		if err := fs.Parse(rest[1:]); err != nil {
			return errReported
		}
		rest = append(rest[:1], fs.Args()...)
	}
	if len(rest) != 1 {
		fs.Usage()
		return errors.New("export takes one pane or conversation id")
	}
	id := rest[0]

	meta, spec, ex, layoutRoot, err := resolveExport(id, env)
	if err != nil {
		return err
	}
	path := f.path
	if path != "" {
		if path, err = filepath.Abs(path); err != nil {
			return err
		}
		for _, project := range []string{meta.ProjectRoot, layoutRoot} {
			if err := record.CheckExportPath(path, project); err != nil {
				return err
			}
		}
	}
	res, err := record.Export(env.stateDir, meta, ex.Follow(spec, meta.Conversation), record.ExportOptions{Path: path})
	switch {
	case errors.Is(err, transcript.ErrNoTranscript):
		return fmt.Errorf("%s has no stored conversation for %s on this machine, so nothing was exported", agentName(spec), meta.Conversation)
	case errors.Is(err, record.ErrNothingToExport):
		return fmt.Errorf("the stored conversation %s has nothing in it to export", meta.Conversation)
	case err != nil:
		return err
	}
	if res.Kept {
		return fmt.Errorf("nothing new was written: the earlier export at %s has events this one would lack (the stored conversation was cut or changed), so it was kept as it was; delete it to have a fresh one", res.Path)
	}
	fmt.Fprintf(out, "exported %d lines (%d prompts, %d messages, %d tool calls) to %s\n", res.Lines, res.Prompts, res.Messages, res.ToolCalls, res.Path)
	if res.Full {
		fmt.Fprintf(out, "the conversation is longer than a transcript can be, so it was cut at its %d MiB cap\n", record.MaxFileBytes>>20)
	}
	if res.Skipped > 0 {
		fmt.Fprintf(out, "%d entries of the stored conversation could not be read and are left out\n", res.Skipped)
	}
	fmt.Fprintln(out, "it may contain secrets: common ones are removed, but that is best effort")
	return nil
}

// resolveExport works out whose conversation id names, and who stores it. The
// last value is the project directory the saved layout gives a pane, which is
// kept out of by a path of the user's own as the conversation's is, and is not
// part of the transcript.
func resolveExport(id string, env exportEnv) (record.Meta, agent.Spec, transcript.Exporter, string, error) {
	specs, defaultID := env.catalog()
	find := func(agentID string) (agent.Spec, bool) {
		if agentID == "" {
			agentID = defaultID
		}
		for _, s := range specs {
			if s.ID == agentID {
				return s, true
			}
		}
		return agent.Spec{}, false
	}

	if sp, ok := env.pane(id); ok {
		spec, ok := find(sp.Agent)
		if !ok {
			return record.Meta{}, agent.Spec{}, nil, "", fmt.Errorf("pane %s runs %q, which is not an agent in this machine's catalog, so its conversation cannot be read", id, sp.Agent)
		}
		ex, ok := transcript.ExporterFor(spec)
		if !ok {
			return record.Meta{}, agent.Spec{}, nil, "", fmt.Errorf("%s stores no conversation Flockdeck can read, so there is nothing to export", agentName(spec))
		}
		conv := sp.Conversation
		if conv == "" {
			conv = sp.ID
		}
		// Whose lines they are is the conversation's, as it is for a recording
		// and for the window's export, and not the saved layout's, which can
		// have a name or a model the pane no longer has.
		root := sp.Root
		if sp.Pane.Root != "" {
			root = sp.Pane.Root
		}
		return record.MetaFor(spec, ex, conv), spec, ex, root, nil
	}

	// Not a pane: a conversation, which is found by looking where each agent
	// keeps them.
	seen := map[string]bool{}
	for _, spec := range specs {
		ex, ok := transcript.ExporterFor(spec)
		if !ok {
			continue
		}
		// Two entries that keep their conversations in one place are one place.
		home := transcript.ClaudeHomeFor(spec)
		if seen[home] {
			continue
		}
		seen[home] = true
		if ex.Cwd(spec, id) == "" && !stored(ex, spec, id) {
			continue
		}
		return record.MetaFor(spec, ex, id), spec, ex, "", nil
	}
	return record.Meta{}, agent.Spec{}, nil, "", fmt.Errorf("no pane or stored conversation %q was found. A pane's id is in the saved layout and a conversation's is its file's name under ~/.claude/projects; "+
		"agents other than Claude Code store nothing Flockdeck can read, so nothing can be exported for them", id)
}

// stored reports whether the agent has a conversation stored under id that says
// anything, which it learns from the first event and no more.
func stored(ex transcript.Exporter, spec agent.Spec, id string) bool {
	_, err := ex.Follow(spec, id).Poll(func(transcript.ExportEvent) error { return errFound })
	return err == nil || errors.Is(err, errFound)
}

var errFound = errors.New("found")
