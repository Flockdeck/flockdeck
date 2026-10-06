package baton

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Limits on what the builder carries over from a conversation.
const (
	// maxGoal and maxReply bound the two pieces of prose taken from the
	// transcript, in bytes.
	maxGoal  = 1500
	maxReply = 3000
	// maxCommands is how many distinct commands are listed, newest first
	// kept, and maxFailed how many failing ones are listed on top of those.
	maxCommands = 15
	maxFailed   = 10
	// maxCommandLine and maxDetail bound one command and one failure note.
	maxCommandLine = 200
	maxDetail      = 120
	// maxFiles bounds each list of files.
	maxFiles = 60
)

// Command is one shell command the agent ran, and how it came out.
type Command struct {
	Text string
	// Outcome is "ok", "failed", "interrupted", or "" when the transcript says
	// nothing (a call with no result yet).
	Outcome string
	// Detail is one line of why it failed, when the transcript has one.
	Detail string
}

// Activity is what a conversation did, in the agent-neutral terms a baton
// needs. It is read from a transcript by ActivityFromEvents or
// ActivityFromEntries and holds nothing else of it: no tool output, no
// thinking, no file contents.
type Activity struct {
	// Prompts are what the person typed, oldest first.
	Prompts []string
	// LastReply is the last thing the agent said.
	LastReply string
	Commands  []Command
	// Refs are the ids of earlier batons that prompts carried, when the builder of
	// the conversation noted them as it read (ActivityBuilder). Prompts that carry
	// one are also noted by Build, for an Activity made another way.
	Refs []string
}

// shellTools names the tools that run a command, across the agents that have a
// transcript reader.
var shellTools = map[string]bool{"bash": true, "powershell": true, "shell": true, "run_command": true}

// What an ActivityBuilder keeps of a conversation however long it is: the first
// few prompts (the goal is the first), the last reply, the references to earlier
// batons that prompts carried, and the newest commands.
const (
	keepPrompts  = 5
	keepRefs     = 50
	keepCommands = 2000
	// maxCommandText cuts a command as it arrives. commandLine takes only the
	// first line of it and cuts that at rawLine, so what is left past this is never
	// read.
	maxCommandText = 32 << 10
)

// ActivityBuilder reduces a conversation to an Activity as its events arrive,
// without keeping the events: a long session is hundreds of thousands of
// them, with their tool output. Use Add for each event and Activity at the end.
type ActivityBuilder struct {
	a      Activity
	byCall map[string]int
}

// NewActivityBuilder makes an empty builder.
func NewActivityBuilder() *ActivityBuilder {
	return &ActivityBuilder{byCall: map[string]int{}}
}

// Add takes one event of the conversation.
func (b *ActivityBuilder) Add(e transcript.ExportEvent) {
	a := &b.a
	switch e.Kind {
	case transcript.ExportPrompt:
		t := strings.TrimSpace(e.Text)
		if t == "" {
			return
		}
		// A prompt that carries an earlier baton is noted as lineage by the builder
		// of the baton, and is not a thing to take a goal from.
		if m := batonRef.FindStringSubmatch(t); m != nil {
			if len(a.Refs) < keepRefs && !contains(a.Refs, m[1]) {
				a.Refs = append(a.Refs, m[1])
			}
			return
		}
		if len(a.Prompts) < keepPrompts {
			// The goal is cut to its bound and a margin before it is scrubbed.
			a.Prompts = append(a.Prompts, clipRaw(t, maxGoal+rawMargin))
		}
	case transcript.ExportMessage:
		if t := strings.TrimSpace(e.Text); t != "" {
			a.LastReply = clipRaw(t, maxReply+rawMargin)
		}
	case transcript.ExportToolCall:
		if !shellTools[strings.ToLower(e.Tool)] {
			return
		}
		in, _ := e.Input.(map[string]any)
		cmd, _ := in["command"].(string)
		if strings.TrimSpace(cmd) == "" {
			return
		}
		if len(a.Commands) >= 2*keepCommands {
			b.dropOldest(len(a.Commands) - keepCommands)
		}
		b.byCall[e.ToolUseID] = len(a.Commands)
		a.Commands = append(a.Commands, Command{Text: clipRaw(cmd, maxCommandText)})
	case transcript.ExportToolResult:
		i, ok := b.byCall[e.ToolUseID]
		if !ok {
			return
		}
		switch {
		case e.Interrupted:
			a.Commands[i].Outcome = "interrupted"
		case e.IsError:
			a.Commands[i].Outcome = "failed"
			a.Commands[i].Detail = firstLine(e.Output)
		default:
			a.Commands[i].Outcome = "ok"
		}
	}
}

// dropOldest forgets the n oldest commands, and the results still waiting for them.
func (b *ActivityBuilder) dropOldest(n int) {
	b.a.Commands = append([]Command(nil), b.a.Commands[n:]...)
	for id, i := range b.byCall {
		if i < n {
			delete(b.byCall, id)
		} else {
			b.byCall[id] = i - n
		}
	}
}

// Activity is what was added.
func (b *ActivityBuilder) Activity() Activity { return b.a }

// ActivityFromEvents reads Activity out of a followed conversation. Full
// command lines come from here, which is why it is preferred where the agent
// can be followed. A caller that has the events one at a time uses
// ActivityBuilder, and need not hold them all.
func ActivityFromEvents(evs []transcript.ExportEvent) Activity {
	b := NewActivityBuilder()
	for _, e := range evs {
		b.Add(e)
	}
	return b.Activity()
}

// ActivityFromEntries reads Activity out of a conversation's stream entries,
// which is the form the chat client's is read in. A command is only as long as
// the entry's label keeps it.
func ActivityFromEntries(entries []transcript.Entry) Activity {
	var a Activity
	for _, e := range entries {
		switch e.Kind {
		case transcript.KindPrompt:
			if t := strings.TrimSpace(e.Text); t != "" {
				a.Prompts = append(a.Prompts, t)
			}
		case transcript.KindReply:
			if t := strings.TrimSpace(e.Markdown); t != "" {
				a.LastReply = t
			}
		case transcript.KindTool:
			if !shellTools[strings.ToLower(e.Name)] {
				continue
			}
			c := Command{Text: strings.TrimSpace(strings.TrimPrefix(e.Label, "Ran "))}
			if c.Text == "" {
				continue
			}
			switch e.Status {
			case transcript.StatusOK:
				c.Outcome = "ok"
			case transcript.StatusError:
				c.Outcome = "failed"
				c.Detail = firstLine(e.Summary)
			}
			a.Commands = append(a.Commands, c)
		}
	}
	return a
}

// buildBudget is how long scrubbing a baton may take in all.
var buildBudget = 5 * time.Second

// rawMargin is how much more than its maximum an input is scrubbed before it is cut.
const rawMargin = 1024

// clipRaw cuts text to at most n bytes, at a character boundary, with nothing added.
func clipRaw(t string, n int) string {
	if len(t) <= n {
		return t
	}
	for n > 0 && !utf8.RuneStart(t[n]) {
		n--
	}
	return t[:n]
}

// rawLine bounds a line taken from a transcript before it is scrubbed. It is
// much longer than any note, so that scrubbing sees a secret whole and the cut
// to a note's length is made on text that has been scrubbed.
const rawLine = 4096

// firstLine is the first non-empty line of s, whole up to rawLine bytes. A note
// built from it is scrubbed and then cut: see clipLine.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return record.Clip(l, rawLine)
		}
	}
	return ""
}

// clipLine cuts an already scrubbed line to a note's length. A cut made before
// scrubbing can leave the front half of a token, which no pattern recognises.
func clipLine(l string) string { return record.Clip(l, maxDetail) }

// GitFacts is the state of the checkout a baton is made from, read from git
// and not from anybody's memory of it.
type GitFacts struct {
	Branch string
	// Head is the short commit id the checkout is at.
	Head string
	// Base is the branch the commits below are counted from ("main", or the
	// upstream), empty when there is none.
	Base string
	// Commits is how many commits HEAD is ahead of Base.
	Commits     int
	Committed   []gitx.FileChange
	Uncommitted []gitx.FileChange
	// Note says why part of this is missing: git is not installed, the folder
	// is not a repository.
	Note string
}

// ReadGit reads the facts for the checkout at dir. It runs git, so it must not
// run on the goroutine that owns the workspace.
func ReadGit(dir string) GitFacts {
	var g GitFacts
	if !gitx.Available() {
		g.Note = "git is not installed, so no files or commits are listed"
		return g
	}
	if !gitx.IsRepo(dir) {
		g.Note = "the working directory is not a git repository, so no files or commits are listed"
		return g
	}
	st := gitx.StatusOf(dir)
	g.Branch, g.Head = st.Branch, st.Head
	if ch, err := gitx.Changes(dir); err == nil {
		g.Uncommitted = ch
	}
	base := gitx.UpstreamOf(dir)
	if base == "" {
		for _, b := range []string{"main", "master"} {
			if b != st.Branch && gitx.BranchExists(dir, b) {
				base = b
				break
			}
		}
	}
	if base != "" {
		if files, n, err := gitx.Since(dir, base); err == nil {
			g.Base, g.Commits, g.Committed = base, n, files
		}
	}
	return g
}

// Pane is what the builder needs to know of the pane a baton is made from.
type Pane struct {
	ID, Name, Agent, Model, Cwd, Branch string
	// Task is what the pane was started to do.
	Task string
}

// BuildInput is everything Build works from.
type BuildInput struct {
	Pane     Pane
	Activity Activity
	Git      GitFacts
	Now      time.Time
	// Derived names the batons this one comes from, before any the transcript
	// itself shows.
	Derived []string
	// Strict is the profile for a baton nobody will read before it is used: an
	// agent's own `spawn --baton`. Commands are listed by program only.
	Strict bool
	// Scrubber carries the values layer one removes. Nil still scrubs by
	// shape and entropy.
	Scrubber *Scrubber
}

// batonRef finds a baton an earlier prompt in the conversation carried.
var batonRef = regexp.MustCompile(`^\s*<baton id="([0-9a-z-]+)"`)

// Build makes a baton from facts. The mechanical sections (files, commands,
// git state) are filled from git and the transcript; Goal and Where things
// stand are a first draft; Decisions, Open questions and Constraints are left
// for a person to write, because nothing here infers them.
//
// The result is already scrubbed.
func Build(in BuildInput) Baton {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	b := Baton{
		ID:        NewID(now),
		Created:   now,
		FromPane:  in.Pane.ID,
		FromAgent: in.Pane.Agent,
		FromModel: in.Pane.Model,
		Cwd:       in.Pane.Cwd,
		Branch:    firstOf(in.Git.Branch, in.Pane.Branch),
		Derived:   append([]string(nil), in.Derived...),
		Sections:  map[Section]string{},
	}
	b.BaseCommit = baseLine(in.Git)

	// Everything taken from the conversation is scrubbed before it is cut to
	// length, since a cut can leave half a token, and again with the whole
	// baton at the end.
	sc := in.Scrubber
	if sc == nil {
		sc = NewScrubber()
	}
	// The whole build has a budget; what is not scrubbed when it is spent is replaced by a
	// mark, never passed on.
	sc = sc.WithDeadline(time.Now().Add(buildBudget))
	// Each input is cut to a bound before it is scrubbed: its own maximum and a margin, so
	// that the cut a note makes after scrubbing falls inside what was scrubbed.
	scrubTo := func(t string, n int) string {
		t, _ = sc.Scrub(CleanText(clipRaw(t, n)))
		return t
	}
	scrub := func(t string) string { return scrubTo(t, rawLine) }

	// A baton an earlier prompt carried is one quoted input, not a thing to
	// pull a goal out of: it is noted as lineage and left out of the prompts.
	for _, id := range in.Activity.Refs {
		if !contains(b.Derived, id) {
			b.Derived = append(b.Derived, id)
		}
	}
	var prompts []string
	for _, p := range in.Activity.Prompts {
		if m := batonRef.FindStringSubmatch(p); m != nil {
			if !contains(b.Derived, m[1]) {
				b.Derived = append(b.Derived, m[1])
			}
			continue
		}
		prompts = append(prompts, p)
	}

	goal := strings.TrimSpace(in.Pane.Task)
	if goal == "" && len(prompts) > 0 {
		goal = prompts[0]
	}
	goal = scrubTo(goal, maxGoal+rawMargin)
	if goal == "" {
		goal = Placeholder
	}
	b.Sections[Goal] = record.Clip(goal, maxGoal)
	b.Title = titleOf(goal, in.Pane.Name)
	if strings.Contains(goal, tooLargeMark) {
		// A goal that was not reached before the budget was spent is not a title.
		b.Title = "Baton from " + firstOf(in.Pane.Name, "a pane")
	}

	b.Sections[Standing] = standing(in.Git, scrubTo(in.Activity.LastReply, maxReply+rawMargin))
	b.Sections[Files] = filesSection(in.Git)
	b.Sections[Commands] = commandsSection(in.Activity.Commands, in.Strict, scrub)
	b.Sections[NotInCheckout] = notInCheckout(in.Git)
	for _, s := range []Section{Decisions, Questions, Constraints} {
		b.Sections[s] = Placeholder
	}

	return sc.ScrubBaton(b)
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// titleOf is the first line of the goal, cut short, or the pane's name.
func titleOf(goal, name string) string {
	t := firstLine(goal)
	if goal == Placeholder || t == "" {
		if name != "" {
			return name
		}
		return "Untitled handoff"
	}
	if r := []rune(t); len(r) > 60 {
		t = string(r[:60]) + "..."
	}
	return t
}

// baseLine says what commit the baton describes: "a1b2c3d (feat/x, 3 commits
// ahead of main)".
func baseLine(g GitFacts) string {
	if g.Head == "" {
		return ""
	}
	var parts []string
	if g.Branch != "" {
		parts = append(parts, g.Branch)
	}
	if g.Base != "" && g.Commits > 0 {
		parts = append(parts, fmt.Sprintf("%d %s ahead of %s", g.Commits, plural(g.Commits, "commit", "commits"), g.Base))
	}
	if len(parts) == 0 {
		return g.Head
	}
	return g.Head + " (" + strings.Join(parts, ", ") + ")"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func standing(g GitFacts, reply string) string {
	var b strings.Builder
	if line := baseLine(g); line != "" {
		fmt.Fprintf(&b, "The checkout was at %s", line)
		if n := len(g.Uncommitted); n > 0 {
			fmt.Fprintf(&b, ", with %d %s uncommitted", n, plural(n, "file", "files"))
		}
		b.WriteString(".\n\n")
	}
	if strings.TrimSpace(reply) == "" {
		b.WriteString("The conversation had no reply from the agent to quote.")
		return b.String()
	}
	b.WriteString("The last thing the agent said:\n\n")
	b.WriteString(quote(record.Clip(strings.TrimSpace(reply), maxReply)))
	return b.String()
}

// quote marks every line of s as quoted, so the agent's own words are not
// taken for the baton's.
func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

func filesSection(g GitFacts) string {
	var b strings.Builder
	list := func(title string, files []gitx.FileChange, label func(gitx.FileChange) string) {
		if len(files) == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(title + "\n\n")
		for i, f := range files {
			if i == maxFiles {
				fmt.Fprintf(&b, "- ... and %d more\n", len(files)-maxFiles)
				break
			}
			fmt.Fprintf(&b, "- %s\n", label(f))
		}
	}
	if g.Base != "" {
		list(fmt.Sprintf("Committed on this branch since %s:", g.Base), g.Committed, func(f gitx.FileChange) string {
			return fileLine(f, "")
		})
	}
	list("Uncommitted:", g.Uncommitted, func(f gitx.FileChange) string {
		return fileLine(f, f.Label)
	})
	if b.Len() == 0 {
		if g.Note != "" {
			return "Not available: " + g.Note + "."
		}
		return "No files differ from the base."
	}
	return strings.TrimRight(b.String(), "\n")
}

// fileLine is one file as the list shows it. A secret file is named and its
// contents are not carried, so it is marked rather than left for a reader to
// wonder about.
func fileLine(f gitx.FileChange, status string) string {
	var meta []string
	if status != "" {
		meta = append(meta, status)
	}
	if f.Added > 0 || f.Removed > 0 {
		meta = append(meta, fmt.Sprintf("+%d -%d", f.Added, f.Removed))
	}
	if secretFile(f.Path) {
		meta = append(meta, "secret file, contents not carried")
	}
	line := "`" + strings.ReplaceAll(f.Path, "`", "'") + "`"
	if len(meta) > 0 {
		line += " (" + strings.Join(meta, ", ") + ")"
	}
	return line
}

func notInCheckout(g GitFacts) string {
	if len(g.Uncommitted) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("These changes were uncommitted in the source checkout when this was made. An agent working in another checkout or a new worktree does not have them:\n\n")
	for i, f := range g.Uncommitted {
		if i == maxFiles {
			fmt.Fprintf(&b, "- ... and %d more\n", len(g.Uncommitted)-maxFiles)
			break
		}
		fmt.Fprintf(&b, "- `%s`\n", strings.ReplaceAll(f.Path, "`", "'"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// commandsSection lists the commands run, repeats collapsed to one line with
// the last result, the newest few plus every one whose last run failed.
func commandsSection(cmds []Command, strict bool, scrub func(string) string) string {
	if len(cmds) == 0 {
		return "No commands recorded."
	}
	type row struct {
		Command
		n    int
		last int
	}
	byText := map[string]*row{}
	var rows []*row
	for i, c := range cmds {
		text := commandLine(c.Text, strict, scrub)
		r := byText[text]
		if r == nil {
			r = &row{}
			byText[text] = r
			rows = append(rows, r)
		}
		r.Command = c
		r.Text = text
		r.n++
		r.last = i
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].last < rows[j].last })
	keep := map[*row]bool{}
	recent := 0
	for i := len(rows) - 1; i >= 0 && recent < maxCommands; i-- {
		keep[rows[i]] = true
		recent++
	}
	failed := 0
	for i := len(rows) - 1; i >= 0 && failed < maxFailed; i-- {
		if rows[i].Outcome == "failed" && !keep[rows[i]] {
			keep[rows[i]] = true
			failed++
		}
	}
	var b strings.Builder
	omitted := 0
	for _, r := range rows {
		if !keep[r] {
			omitted++
			continue
		}
		fmt.Fprintf(&b, "- `%s`", strings.ReplaceAll(r.Text, "`", "'"))
		var notes []string
		if r.n > 1 {
			notes = append(notes, fmt.Sprintf("run %d times, last:", r.n))
		}
		switch r.Outcome {
		case "failed":
			notes = append(notes, "FAILED")
		case "interrupted":
			notes = append(notes, "interrupted by the user")
		case "ok":
			if r.n > 1 {
				notes = append(notes, "ok")
			}
		}
		if len(notes) > 0 {
			b.WriteString(" - " + strings.Join(notes, " "))
		}
		if r.Outcome == "failed" && r.Detail != "" && !strict {
			b.WriteString(": " + clipLine(scrub(r.Detail)))
		}
		b.WriteString("\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "- ... and %d older %s\n", omitted, plural(omitted, "command", "commands"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// commandLine is the first line of a command, cut short, and with its
// arguments dropped in the strict profile.
//
// scrub is applied to what is kept, and before any cut. In the strict profile
// the arguments are dropped first, so that what is left is a program name and
// not a mark standing in for an argument.
func commandLine(cmd string, strict bool, scrub func(string) string) string {
	if strict {
		line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(cmd), "\n", 2)[0])
		return scrub(Strictly(line))
	}
	cmd = scrub(cmd)
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(cmd), "\n", 2)[0])
	multi := strings.Contains(strings.TrimSpace(cmd), "\n")
	line = record.Clip(line, maxCommandLine)
	if multi {
		line += " ..."
	}
	return line
}

// subcommands are the words kept after a program in the strict profile. They
// name an action, and an argument that is a word of somebody's choosing is not
// one of them.
var subcommands = map[string]bool{
	"add": true, "build": true, "check": true, "checkout": true, "commit": true, "diff": true,
	"fetch": true, "fmt": true, "generate": true, "get": true, "install": true, "lint": true,
	"list": true, "log": true, "merge": true, "mod": true, "pull": true, "push": true,
	"rebase": true, "run": true, "show": true, "status": true, "test": true, "vet": true,
}

// Strictly drops a command's arguments: it keeps the program, and the action
// word after it when that is a known one, and says something was dropped. A
// command line is where a pasted token lives, and an agent's own baton is one
// nobody reads before it is used.
//
// The line is read for its quoting, so that FOO="a b" deploy is an assignment
// and a program, and for the places a command can be nested: a bracket, $( or a
// backtick starts one, and `env -S` takes a whole command in a string. Leading
// VAR=value words, whichever of these they are in, are dropped, and a word that
// is not understood is dropped rather than kept.
func Strictly(cmd string) string {
	// A mark that was put where a secret was has a space in it; it is one word.
	words := shellWords(markRe.ReplaceAllString(cmd, "[redacted]"))
	for range 8 {
		words = dropAssignments(words)
		if len(words) == 0 || baseName(unquote(words[0])) != "env" {
			break
		}
		words = envArguments(words[1:])
	}
	if len(words) == 0 {
		return ""
	}
	kept := []string{baseName(unquote(words[0]))}
	rest := words[1:]
	if len(rest) > 0 && subcommands[unquote(rest[0])] {
		kept = append(kept, unquote(rest[0]))
		rest = rest[1:]
	}
	if len(rest) > 0 {
		kept = append(kept, "...")
	}
	return strings.Join(kept, " ")
}

// shellWords splits a command line into words at spaces outside quotes. A
// bracket, a brace, a backtick, $( and the separators ; | & also end a word, and
// are not part of any, so a command nested in them reads as a command of its
// own. Quotes stay on the words they are in, and $'...' (where a backslash
// escapes a quote) is one; an unclosed one runs to the end. A here-document
// operator (<<, <<-, <<<) is a word like another, and the body is on the lines
// after the first, which are not read.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	have := false
	flush := func() {
		if have {
			words = append(words, cur.String())
			cur.Reset()
			have = false
		}
	}
	var quote byte
	ansi := false // inside $'...', where a backslash escapes a quote
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			cur.WriteByte(c)
			if c == '\\' && (quote == '"' || ansi) && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else if c == quote {
				quote, ansi = 0, false
			}
		case c == '$' && i+1 < len(s) && s[i+1] == '\'':
			quote, ansi, have = '\'', true, true
			cur.WriteString("$'")
			i++
		case c == '\'' || c == '"':
			quote, have = c, true
			cur.WriteByte(c)
		case c == '\\' && i+1 < len(s):
			cur.WriteByte(c)
			i++
			cur.WriteByte(s[i])
			have = true
		case c == ' ' || c == '\t':
			flush()
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			flush()
			i++
		case strings.IndexByte("();|&{}`", c) >= 0:
			flush()
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	flush()
	return words
}

// unquote is a word with its quotes taken out. Backslashes stay: on Windows
// they are the separator in C:\\tools\\deploy.exe.
func unquote(w string) string {
	return strings.NewReplacer(`"`, "", `'`, "").Replace(w)
}

// envAssign is a word that sets a variable for the command that follows it.
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func dropAssignments(words []string) []string {
	for len(words) > 0 && envAssign.MatchString(words[0]) {
		words = words[1:]
	}
	return words
}

// envArguments reads what follows env: its flags, which it drops, the
// assignments, and the command. -S (split string) takes a whole command line as
// one argument, which is read again as words. A flag that is not known is
// dropped alone, and what follows it is read as the command.
func envArguments(words []string) []string {
	for len(words) > 0 {
		w := unquote(words[0])
		switch {
		case envAssign.MatchString(words[0]):
			words = words[1:]
		case w == "-S" || w == "--split-string":
			if len(words) < 2 {
				return nil
			}
			words = append(shellWords(unquote(words[1])), words[2:]...)
		case strings.HasPrefix(w, "--split-string="):
			words = append(shellWords(strings.TrimPrefix(w, "--split-string=")), words[1:]...)
		case strings.HasPrefix(w, "-S") && len(w) > 2:
			words = append(shellWords(w[2:]), words[1:]...)
		case w == "-u" || w == "--unset" || w == "-C" || w == "--chdir" || w == "-a" || w == "--argv0":
			if len(words) < 2 {
				return nil
			}
			words = words[2:]
		case w == "--":
			return words[1:]
		case strings.HasPrefix(w, "-"):
			words = words[1:]
		default:
			return words
		}
	}
	return words
}

// baseName is a program's name without its folder, whichever separator was used.
func baseName(prog string) string {
	if i := strings.LastIndexAny(prog, `/\\`); i >= 0 {
		return prog[i+1:]
	}
	return prog
}

// cmdLineRe is a line of the Commands section as commandsSection writes it.
var cmdLineRe = regexp.MustCompile("^(- `)([^`]*)(`)(.*)$")

// Harden applies the strict profile to a baton that was not built under it, so
// that a reference an agent names is held to the same rule as one it makes:
// the Commands section keeps programs and drops arguments. Lines that are not
// in the form Build writes are dropped, since they cannot be trusted to have
// no arguments in them.
func Harden(b Baton) Baton {
	text := b.Sections[Commands]
	if strings.TrimSpace(text) == "" {
		return b
	}
	var out []string
	for _, l := range strings.Split(text, "\n") {
		m := cmdLineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		detail := m[4]
		if i := strings.Index(detail, ": "); i >= 0 && strings.Contains(detail[:i], "FAILED") {
			detail = detail[:i]
		}
		out = append(out, m[1]+Strictly(m[2])+m[3]+detail)
	}
	if len(out) == 0 {
		out = []string{"No commands recorded."}
	}
	return b.Set(Commands, strings.Join(out, "\n"))
}

// secretFile reports whether a path names a file that holds secrets: an .env
// file or a key. It is a short list on purpose. internal/secretname has the
// full one, but its table of file names is what antivirus on a developer's
// machine reads as a credential stealer, and a binary that links it, a test
// binary included, can be quarantined.
func secretFile(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(path)))
	return strings.HasPrefix(base, ".env") || keyPathRe.MatchString(base)
}
