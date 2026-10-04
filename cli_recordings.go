package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/store"
)

// runRecordings implements `flockdeck recordings`: it lists the transcripts
// panes have recorded, newest first, or with -dir prints the folder they are
// kept in; `recordings export` makes one from an agent's stored conversation. It reads the state directory directly, so it works with no
// instance running -- which is when somebody goes looking for one.
func runRecordings(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "export" {
		return exportRecording(args[1:], out, realExportEnv())
	}
	return listRecordings(args, out, store.Dir)
}

// recordingsFlags are the flags of `flockdeck recordings`.
type recordingsFlags struct {
	dir  bool
	json bool
}

func recordingsFlagSet(f *recordingsFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("recordings", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.BoolVar(&f.dir, "dir", false, "print the folder the recordings are kept in, and nothing else")
	fs.BoolVar(&f.json, "json", false, "print the list as JSON, one object per recording")
	return fs
}

// listRecordings is runRecordings over a state directory it is given, so a
// test can point it at a temporary one.
func listRecordings(args []string, out io.Writer, stateDir func() (string, error)) error {
	var f recordingsFlags
	fs := recordingsFlagSet(&f)
	dir, asJSON := &f.dir, &f.json
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: flockdeck recordings [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Lists the transcripts panes have recorded, newest first: when it started, the\n")
		fmt.Fprintf(os.Stderr, "project, the conversation's id (its first eight characters), its size and the file.\n")
		fmt.Fprintf(os.Stderr, "Exports are not listed: they are in each project's exports folder.\n\n")
		fmt.Fprintf(os.Stderr, "A pane records only while recording is on for it (the record button in its\n")
		fmt.Fprintf(os.Stderr, "header, or Start recording in the command palette), and nothing is recorded by\n")
		fmt.Fprintf(os.Stderr, "default. A pane that was not recorded can still have a transcript made from\n")
		fmt.Fprintf(os.Stderr, "its agent's stored conversation: see `flockdeck recordings export -h`.\n\n")
		fmt.Fprintf(os.Stderr, "Each recording is a JSON Lines file. The format is documented in\n")
		fmt.Fprintf(os.Stderr, "docs/recording-format.md in the Flockdeck repository, with a JSON Schema in\n")
		fmt.Fprintf(os.Stderr, "docs/recording-line.schema.json.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errReported
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return fmt.Errorf("recordings takes no arguments, only flags")
	}
	root, err := record.Root(stateDir)
	if err != nil {
		return err
	}
	if *dir {
		fmt.Fprintln(out, root)
		return nil
	}
	infos, err := record.List(stateDir)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		for _, in := range infos {
			if err := enc.Encode(in); err != nil {
				return err
			}
		}
		return nil
	}
	if len(infos) == 0 {
		fmt.Fprintln(out, "no recordings; turn on recording for a pane to make one (folder:", root+")")
		return nil
	}
	for _, in := range infos {
		when := in.Modified.Local().Format("2006-01-02 15:04")
		if t, err := time.Parse(time.RFC3339Nano, in.Started); err == nil {
			when = t.Local().Format("2006-01-02 15:04")
		}
		conversation := in.Conversation
		if len(conversation) > 8 {
			conversation = conversation[:8]
		}
		if conversation == "" {
			conversation = "-"
		}
		project := in.Project
		if project == "" {
			project = in.Folder
		}
		fmt.Fprintf(out, "%s  %-20s %-20s %8s  %s\n", when, project, conversation, byteSize(in.Size), in.Path)
	}
	return nil
}

// byteSize is a size in the units a person reads.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
