package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmwri/flockdeck/internal/baton"
)

// batonFlags are the flags of `flockdeck baton show`.
type batonFlags struct {
	path bool
}

func batonShowFlagSet(f *batonFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("baton show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.BoolVar(&f.path, "path", false, "print the file the baton is kept in, and nothing else")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: flockdeck baton show [-path] <baton-id>\n\n")
		fmt.Fprintf(os.Stderr, "Prints a baton, the handoff another agent started this work from. A pane that\n")
		fmt.Fprintf(os.Stderr, "was started from one is told its id; `flockdeck baton list` shows them all.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs
}

// runBaton implements `flockdeck baton`: show prints one baton and list lists
// them, newest first. Batons are files in the state directory, so it needs no
// running instance, and works from inside a pane or outside one.
func runBaton(args []string, out io.Writer) error {
	s, err := baton.Open()
	if err != nil {
		return err
	}
	return runBatonIn(s, args, out)
}

// runBatonIn is runBaton over a store it is given, so a test can point it at a
// temporary one.
func runBatonIn(s *baton.Store, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "-help" || args[0] == "--help" {
		batonUsage()
		if len(args) == 0 {
			return errReported
		}
		return nil
	}
	switch args[0] {
	case "list":
		return listBatons(s, args[1:], out)
	case "show":
		return showBaton(s, args[1:], out)
	}
	batonUsage()
	return fmt.Errorf("baton has no subcommand %q", args[0])
}

func batonUsage() {
	fmt.Fprintf(os.Stderr, "Usage: flockdeck baton list\n")
	fmt.Fprintf(os.Stderr, "       flockdeck baton show [-path] <baton-id>\n\n")
	fmt.Fprintf(os.Stderr, "A baton is a short document one agent hands to the next: what the work is for,\n")
	fmt.Fprintf(os.Stderr, "where it stands, the files and commands, and what must hold. They are kept in\n")
	fmt.Fprintf(os.Stderr, "Flockdeck's state directory, on this machine only. Start an agent from one with\n")
	fmt.Fprintf(os.Stderr, "`flockdeck spawn -baton <self | pane-id | baton-id | file> <task>`.\n")
}

func listBatons(s *baton.Store, args []string, out io.Writer) error {
	if len(args) > 0 {
		batonUsage()
		return fmt.Errorf("baton list takes no arguments")
	}
	all, err := s.List()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Fprintln(out, "no batons; make one from a pane with Make baton in the command palette (folder:", s.Dir()+")")
		return nil
	}
	for _, b := range all {
		when := "-"
		if !b.Created.IsZero() {
			when = b.Created.Local().Format("2006-01-02 15:04")
		}
		agent := b.FromAgent
		if agent == "" {
			agent = "-"
		}
		// Printed to a terminal, so nothing in a stored file may move the cursor.
		fmt.Fprint(out, baton.CleanText(fmt.Sprintf("%s  %s  %-12s %s\n", b.ID, when, agent, oneLine(b.Title))))
	}
	return nil
}

func showBaton(s *baton.Store, args []string, out io.Writer) error {
	var f batonFlags
	fs := batonShowFlagSet(&f)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errReported
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errReported
	}
	id := strings.TrimSpace(fs.Arg(0))
	b, err := s.Load(id)
	if errors.Is(err, baton.ErrNotFound) {
		return fmt.Errorf("no baton %q; flockdeck baton list shows what there is", id)
	}
	if err != nil {
		return err
	}
	s.Touch(id)
	if f.path {
		fmt.Fprintln(out, s.Path(id))
		return nil
	}
	fmt.Fprint(out, baton.CleanText(baton.Render(b)))
	return nil
}

// batonRef turns what `spawn -baton` was given into the reference the
// application resolves: "self" and a baton id as they are, a file that exists
// as "path:" and its absolute path, and anything else as it was written, which
// the application takes for a pane id and refuses if it is none.
func batonRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "self" || baton.ValidID(ref) {
		return ref
	}
	if fi, err := os.Stat(ref); err == nil && !fi.IsDir() {
		if abs, err := filepath.Abs(ref); err == nil {
			return "path:" + abs
		}
	}
	return ref
}

// oneLine keeps a title to the one line a list row has.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
