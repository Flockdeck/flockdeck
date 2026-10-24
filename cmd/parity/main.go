// Command parity lists what the old interface does that the new one has to, and
// where each of those things stands. It reads two things from the source and
// compares them:
//
//   - the actions of the key table (ui/help/keys.go), against the ones the
//     new shell runs (IMPLEMENTED in shell/app.js and the actions Live.js adds);
//   - the control-socket commands the old interface sent (the frozen list in
//     oldcommands.txt; the old interface itself was deleted), against the ones the
//     new interface sends (ui/src).
//
// Everything in either list that the new interface does not do yet must be
// classified in classify.go: replaced by a command it sends, cut on purpose, or
// owned by a task on the board. The test in this package fails on an old action
// that is none of these, so a thing the old interface does cannot be dropped by
// being forgotten. docs/parity.md is the result, and the test fails when it is
// out of date.
//
// Usage, from the root of the repository:
//
//	go run ./cmd/parity -w     write docs/parity.md
//	go run ./cmd/parity        print it
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/ui/help"
)

const (
	oldList   = "cmd/parity/oldcommands.txt"
	nextShell = "ui/src/shell/app.js"
	nextLive  = "ui/src/shell/Live.js"
	nextDir   = "ui/src"
	serverDir = "internal/server"
	outFile   = "docs/parity.md"
)

func main() {
	write := flag.Bool("w", false, "write "+outFile+" instead of printing it")
	root := flag.String("root", ".", "the root of the repository")
	flag.Parse()
	out, err := generate(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parity:", err)
		os.Exit(1)
	}
	if !*write {
		fmt.Print(out)
		return
	}
	if err := os.WriteFile(filepath.Join(*root, outFile), []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "parity:", err)
		os.Exit(1)
	}
}

// Facts are what the sources say.
type Facts struct {
	// OldCommands are the commands the old interface sends, with how many places
	// send each (a literal) or 0 for one only reached through a dynamic site.
	OldCommands map[string]int
	// DynamicSites are the places the old interface builds a command name at run
	// time, as written, so a new one is noticed.
	DynamicSites []string
	// NewCommands are the commands the new interface sends.
	NewCommands map[string]bool
	// Implemented are the key-table actions the new shell runs.
	Implemented map[string]bool
}

var (
	literalCmd = regexp.MustCompile(`\bcmd:\s*"([A-Za-z0-9]+)"`)
	// Any other value of cmd: a variable, a ternary, a single-quoted string. The
	// value runs to the next comma, closing brace or end of line.
	dynamicCmd  = regexp.MustCompile(`\bcmd:\s*([^"\s{][^,}]*)`)
	quoted      = regexp.MustCompile(`"([A-Za-z0-9]+)"`)
	prefCmdLine = regexp.MustCompile(`const PREF_CMD = \{([^}]*)\}`)
	implemented = regexp.MustCompile(`export const IMPLEMENTED = \[([^\]]*)\]`)
	liveAction  = regexp.MustCompile(`(?m)^    ([A-Za-z]+):`)
)

// isComment is whether a line of JavaScript is a comment, which may name a
// command without sending it.
func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*")
}

// scanCommands reads the commands a JavaScript source sends: the literal names,
// and the text of every place that does not write one.
func scanCommands(src string) (literals map[string]int, dynamic []string) {
	literals = map[string]int{}
	for _, line := range strings.Split(src, "\n") {
		if isComment(line) {
			continue
		}
		for _, m := range literalCmd.FindAllStringSubmatch(line, -1) {
			literals[m[1]]++
		}
		for _, m := range dynamicCmd.FindAllStringSubmatch(line, -1) {
			dynamic = append(dynamic, strings.TrimSpace(m[1]))
		}
	}
	sort.Strings(dynamic)
	return literals, dynamic
}

// readFile reads a file under root.
func readFile(root, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	return string(b), err
}

// gather reads the sources.
func gather(root string) (Facts, error) {
	f := Facts{NewCommands: map[string]bool{}, Implemented: map[string]bool{}}

	old, err := readFile(root, oldList)
	if err != nil {
		return f, err
	}
	f.OldCommands, f.DynamicSites = parseOldList(old)

	err = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(nextDir)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".js") && !strings.HasSuffix(p, ".mjs") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lits, _ := scanCommands(string(b))
		for name := range lits {
			f.NewCommands[name] = true
		}
		return nil
	})
	if err != nil {
		return f, err
	}

	shell, err := readFile(root, nextShell)
	if err != nil {
		return f, err
	}
	if m := implemented.FindStringSubmatch(shell); m != nil {
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			f.Implemented[q[1]] = true
		}
	}
	live, err := readFile(root, nextLive)
	if err != nil {
		return f, err
	}
	// The actions Live.js hands the shell are the arrow functions in the one
	// object that useMemo builds, from "const actions" to its dependency list.
	if i := strings.Index(live, "const actions = useMemo("); i >= 0 {
		block := live[i:]
		if j := strings.Index(block, "}), ["); j > 0 {
			block = block[:j]
		}
		for _, m := range liveAction.FindAllStringSubmatch(block, -1) {
			f.Implemented[m[1]] = true
		}
	}
	return f, nil
}

// serverKnows is the set of command names the server source mentions as a string
// literal, which a command the old interface sends must be one of: it is how a
// mistake in the scan above shows.
func serverKnows(root string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(serverDir)))
	if err != nil {
		return nil, err
	}
	lit := regexp.MustCompile(`case "([A-Za-z0-9]+)"`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(serverDir), e.Name()))
		if err != nil {
			return nil, err
		}
		for _, m := range lit.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	return out, nil
}

// Row is one line of a table of the result.
type Row struct {
	Name   string
	Detail string // the label and key of an action, or where a command is sent
	Status string // done, replaced, cut or planned
	Note   string // the replacement, the reason, or the task and what it covers
	Task   string // the task that owns a planned one
}

// rows classifies every action and command.
func rows(f Facts) (keyRows, cmdRows []Row, problems []string) {
	for _, k := range help.Keys {
		detail := k.Label
		if k.Keys != "" {
			detail += " (" + k.Keys + ")"
		}
		r := Row{Name: k.ID, Detail: detail}
		v, has := keyVerdicts[k.ID]
		switch {
		case f.Implemented[k.ID]:
			r.Status = "done"
			if has && v.Kind == Cut {
				problems = append(problems, fmt.Sprintf("action %s is cut in classify.go but the new interface runs it", k.ID))
			}
		case !has:
			r.Status = "unclassified"
			problems = append(problems, fmt.Sprintf("action %s (%s) is not run by the new interface and is not classified in classify.go", k.ID, k.Label))
		default:
			r.Status, r.Note = v.describe()
			r.Task = v.To
		}
		keyRows = append(keyRows, r)
	}

	names := make([]string, 0, len(f.OldCommands))
	for n := range f.OldCommands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := Row{Name: n}
		v, has := cmdVerdicts[n]
		switch {
		case f.NewCommands[n]:
			r.Status = "done"
		case !has:
			r.Status = "unclassified"
			problems = append(problems, fmt.Sprintf("command %s is sent by the old interface, not by the new one, and is not classified in classify.go", n))
		default:
			r.Status, r.Note = v.describe()
			r.Task = v.To
			if v.Kind == Replaced && !f.NewCommands[v.To] {
				problems = append(problems, fmt.Sprintf("command %s is said to be replaced by %s, which the new interface does not send", n, v.To))
			}
		}
		cmdRows = append(cmdRows, r)
	}
	return keyRows, cmdRows, problems
}

// generate is the text of docs/parity.md.
func generate(root string) (string, error) {
	f, err := gather(root)
	if err != nil {
		return "", err
	}
	keyRows, cmdRows, _ := rows(f)

	var b strings.Builder
	b.WriteString("# Parity of the new interface with the old\n\n")
	b.WriteString("This file is written by `go run ./cmd/parity -w`. Do not edit it: change `cmd/parity/classify.go` or the interface and write it again. A test in `cmd/parity` fails when this file is out of date, and when the old interface does something that is neither done, replaced, cut nor owned by a task.\n\n")
	b.WriteString("It compares two lists the sources hold: the actions of the key table (`ui/help/keys.go`) against what the new shell runs, and the control-socket commands the old interface sent (the frozen list in `cmd/parity/oldcommands.txt`; the old interface was deleted) against the ones the new interface sends. It does not look at the pane sockets or the pages the old interface fetches, and it cannot see an old feature that has no action or command of its own. See [what this does not cover](#what-this-does-not-cover).\n\n")

	b.WriteString("## Summary\n\n")
	b.WriteString("| | Actions | Commands |\n| --- | --- | --- |\n")
	for _, s := range []string{"done", "replaced", "cut", "planned", "unclassified"} {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", s, count(keyRows, s), count(cmdRows, s))
	}
	fmt.Fprintf(&b, "| total | %d | %d |\n\n", len(keyRows), len(cmdRows))
	b.WriteString("Done means the new interface runs the action or sends the command under that name. Replaced means it sends another command for it. Cut is on purpose. Planned names the task on the board that owns it.\n\n")

	b.WriteString("## Cut on purpose\n\n")
	b.WriteString("The old interface let a person tile, split, move and broadcast to panes. The new one shows a team as a graph and does not do these. Nothing else is cut.\n\n")
	b.WriteString("| Kind | Name | Why |\n| --- | --- | --- |\n")
	for _, r := range keyRows {
		if r.Status == "cut" {
			fmt.Fprintf(&b, "| action | `%s` | %s |\n", r.Name, r.Note)
		}
	}
	for _, r := range cmdRows {
		if r.Status == "cut" {
			fmt.Fprintf(&b, "| command | `%s` | %s |\n", r.Name, r.Note)
		}
	}
	b.WriteString("\n")

	b.WriteString("## Planned, by task\n\n")
	type pair struct{ actions, commands []string }
	byTask := map[string]*pair{}
	add := func(rs []Row, command bool) {
		for _, r := range rs {
			if r.Status != "planned" {
				continue
			}
			p := byTask[r.Task]
			if p == nil {
				p = &pair{}
				byTask[r.Task] = p
			}
			if command {
				p.commands = append(p.commands, "`"+r.Name+"`")
			} else {
				p.actions = append(p.actions, "`"+r.Name+"`")
			}
		}
	}
	add(keyRows, false)
	add(cmdRows, true)
	tasks := make([]string, 0, len(byTask))
	for t := range byTask {
		tasks = append(tasks, t)
	}
	sort.Strings(tasks)
	b.WriteString("| Task | Actions | Commands |\n| --- | --- | --- |\n")
	for _, t := range tasks {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", t, strings.Join(byTask[t].actions, ", "), strings.Join(byTask[t].commands, ", "))
	}
	b.WriteString("\n")

	b.WriteString("## Known gaps\n\nThe old interface was deleted with these missing from the new one. They are chosen, not forgotten.\n\n")
	for _, g := range knownGaps {
		fmt.Fprintf(&b, "- %s: %s\n", g.What, g.Detail)
	}
	b.WriteString("\n")
	b.WriteString("## Safety and security controls\n\n")
	b.WriteString("These are safety and security controls the new interface has to send. The list is empty: autoReview, autoReviewDefault and jevKey are built. A command added to blocksRetirement in classify.go appears here with its owner and the task that waits for it (T162 was the flip).\n\n")
	b.WriteString("| Command | Owner | Gate | What it is |\n| --- | --- | --- | --- |\n")
	for _, n := range blocksRetirement {
		for _, r := range cmdRows {
			if r.Name == n {
				fmt.Fprintf(&b, "| `%s` | %s | T162 | %s |\n", r.Name, r.Task, strings.TrimPrefix(r.Note, r.Task+". "))
			}
		}
	}
	b.WriteString("\n")

	b.WriteString("## Actions of the key table\n\n")
	b.WriteString("| Action | What it is | Status | |\n| --- | --- | --- | --- |\n")
	for _, r := range keyRows {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", r.Name, strings.ReplaceAll(r.Detail, "|", `\|`), r.Status, r.Note)
	}
	b.WriteString("\n## Commands the old interface sends\n\n")
	b.WriteString("| Command | Status | |\n| --- | --- | --- |\n")
	for _, r := range cmdRows {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", r.Name, r.Status, r.Note)
	}

	var only []string
	for n := range f.NewCommands {
		if _, old := f.OldCommands[n]; !old {
			only = append(only, "`"+n+"`")
		}
	}
	sort.Strings(only)
	b.WriteString("\n## Commands only the new interface sends\n\n")
	b.WriteString(strings.Join(only, ", ") + "\n\n")

	b.WriteString("## What this does not cover\n\n")
	b.WriteString("- The pane sockets (`/ws/pty`, `/ws/artifacts`) and the pages the old interface fetches (`help.json` and the like). Only the control socket's `cmd` names are compared.\n")
	b.WriteString("- A feature with no action or command of its own, such as a dialog that opens from a click on a title, or how a screen reader hears a row. This list says a command exists, not that its screen is as good.\n")
	b.WriteString("- A command the old interface builds from a name it was sent. The places that do are listed in `classify.go` (`dynamicSites`) and the test fails when a new one appears.\n")
	b.WriteString("- Whether a command classified as replaced does everything the old one did.\n")
	return b.String(), nil
}

func count(rs []Row, status string) int {
	n := 0
	for _, r := range rs {
		if r.Status == status {
			n++
		}
	}
	return n
}

// parseOldList reads oldcommands.txt: one "name count" line for each command the
// old interface sent (count is how many places sent it, 0 for one reached only
// through a dynamic site), then a line "#dynamic" and the dynamic sites as
// written. The list was taken from the old interface's source when it was
// deleted and is not regenerated.
func parseOldList(src string) (map[string]int, []string) {
	cmds := map[string]int{}
	var dyn []string
	inDyn := false
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		switch {
		case line == "":
		case line == "#dynamic":
			inDyn = true
		case inDyn:
			dyn = append(dyn, line)
		default:
			name, n, _ := strings.Cut(line, " ")
			count := 0
			fmt.Sscanf(n, "%d", &count)
			cmds[name] = count
		}
	}
	return cmds, dyn
}
