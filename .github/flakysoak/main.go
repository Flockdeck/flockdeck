// flakysoak reads what the nightly flaky-test soak's `go test -json` runs
// printed and keeps the "Flaky tests" tracking issue up to date. It does no
// network I/O: the workflow gives it the issue's current body and applies the
// outcome it writes, so that nothing a test printed is ever interpolated into
// a shell, and everything here can be tested with fixtures.
//
//	flakysoak parse  -os NAME -count N -exit CODE -in stream.json -stderr err.log -out result.json
//	flakysoak report -results DIR -expect a,b,c -prev-body FILE -exists -open \
//	                 -run-url URL -date YYYY-MM-DD -close-after 3 -out DIR
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: flakysoak parse|report ...")
	}
	var err error
	switch os.Args[1] {
	case "parse":
		err = runParse(os.Args[2:])
	case "report":
		err = runReport(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "flakysoak:", msg)
	os.Exit(2)
}

func runParse(args []string) error {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	osName := fs.String("os", "", "the runner's OS label")
	count := fs.Int("count", 0, "the -count the tests ran with")
	in := fs.String("in", "", "go test -json output")
	out := fs.String("out", "result.json", "where to write the result")
	exit := fs.Int("exit", 0, "go test's exit status")
	stderr := fs.String("stderr", "", "go test's stderr, if kept")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()
	res, err := Parse(f, *osName, *count)
	if err != nil {
		return err
	}
	var errText string
	if *stderr != "" {
		if b, err := os.ReadFile(*stderr); err == nil {
			errText = string(b)
		}
	}
	addExitFailure(&res, *exit, errText)
	data, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	// The markdown goes to stdout, for the job's summary page.
	fmt.Print(Summary(res))
	return nil
}

// Summary is what a job's own summary page shows.
func Summary(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s: %d tests, -count=%d\n\n", r.OS, r.Tests, r.Count)
	if r.Unparsed > 0 {
		fmt.Fprintf(&b, "%d lines of output were not test events.\n\n", r.Unparsed)
	}
	if len(r.Failures) == 0 {
		b.WriteString("No failures.\n")
		return b.String()
	}
	b.WriteString("| Test | Failed of runs |\n|---|---|\n")
	for _, f := range r.Failures {
		fmt.Fprintf(&b, "| `%s` | %d of %d |\n", short(f.Package, f.Test), f.Failed, f.Runs)
	}
	return b.String()
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	dir := fs.String("results", "", "directory holding result.json files, at any depth")
	expect := fs.String("expect", "", "comma-separated OSes that ought to have reported")
	prev := fs.String("prev-body", "", "the tracking issue's current body, if it exists")
	exists := fs.Bool("exists", false, "the tracking issue exists")
	open := fs.Bool("open", false, "the tracking issue is open")
	runURL := fs.String("run-url", "", "this run's URL")
	date := fs.String("date", "", "the night's date, UTC")
	closeAfter := fs.Int("close-after", 3, "clean nights in a row that close the issue")
	out := fs.String("out", ".", "directory for outcome.json, body.md, comment.md")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var results []Result
	err := filepath.WalkDir(*dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "result.json" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var r Result
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		results = append(results, r)
		return nil
	})
	if err != nil {
		return err
	}
	in := Input{
		Exists: *exists, Open: *open, Results: results,
		Night: Night{Date: *date, RunURL: *runURL}, CloseAfter: *closeAfter,
	}
	for _, o := range strings.Split(*expect, ",") {
		if o = strings.TrimSpace(o); o != "" {
			in.Expect = append(in.Expect, o)
		}
	}
	if *exists && *prev != "" {
		body, err := os.ReadFile(*prev)
		if err != nil {
			return err
		}
		if st, ok := ParseState(string(body)); ok {
			in.Prev = &st
		} else {
			fmt.Fprintln(os.Stderr, "flakysoak: the issue has no readable state; starting from an empty one")
		}
	}
	o := Decide(in)
	meta, _ := json.Marshal(o)
	files := map[string]string{"outcome.json": string(meta) + "\n", "body.md": o.Body, "comment.md": o.Comment}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(*out, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("action=%s\n", o.Action)
	return nil
}
