package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmwri/agent-wrapper/internal/gitx"
	"github.com/jmwri/agent-wrapper/internal/layout"
	"github.com/jmwri/agent-wrapper/internal/session"
)

// recentOutputBytes is how much of a pane's output the task extractor reads.
// A plan is near the end of what an agent just said.
const recentOutputBytes = 64 << 10

// maxTasks caps a fan-out. Each task is a real agent with a real terminal, and
// beyond this the machine, not the idea, becomes the limit.
const maxTasks = 12

// MaxTasks is maxTasks for callers that start the agents themselves, so the
// limit is one number rather than one per fan-out path.
const MaxTasks = maxTasks

// maxTaskRunes bounds a task. Longer than this and the line is a paragraph that
// happens to begin with a dash, not a job to hand to an agent.
const maxTaskRunes = 600

// minTaskRunes is the shortest thing worth starting an agent for. Below it a
// line is a fragment: the tail of a wrapped row, or a one-word status.
const minTaskRunes = 8

// ExtractTasks finds the work items in a plan.
//
// A lead agent asked for a plan answers with a list, so list markers are the
// signal: bullets, numbers and checkboxes. Everything else is prose. The result
// is a suggestion the user edits before anything is started, so it is better to
// offer a few plausible lines than to guess cleverly.
func ExtractTasks(text string) []string {
	items, cue := listItems(text)

	// An agent's answer is rarely only its plan: it surveys what it read, notes
	// what it found, and then says what it would do. All of that is bulleted,
	// and only the last part is work. When the answer says where the plan
	// starts, take it at its word — but only while that still leaves something.
	if cue >= 0 {
		var planned []item
		for _, it := range items {
			if it.line > cue {
				planned = append(planned, it)
			}
		}
		if len(planned) > 0 {
			items = planned
		}
	}

	// Only the shallowest entries are tasks. Anything nested under one is
	// detail about how to do that job, not a job of its own, and fanning out
	// over both hands the same work to two agents.
	base := -1
	for _, it := range items {
		if base < 0 || it.indent < base {
			base = it.indent
		}
	}

	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		// A single space of drift is alignment, not nesting.
		if it.indent > base+1 {
			continue
		}
		task := tidyTask(it.text)
		if !isTask(task) {
			continue
		}
		key := strings.ToLower(task)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, task)
		if len(out) >= maxTasks {
			break
		}
	}
	return out
}

// item is one list entry, how far it was indented, and which line it came from.
type item struct {
	indent int
	line   int
	text   string
}

// listItems reads the list entries out of text, and reports the line on which
// the text last announced a plan.
//
// An entry that carries on over the lines below it is put back together: a
// terminal breaks a long bullet across rows, and markdown lets one run on over
// indented lines. Keeping only the first row of either would hand an agent a
// task that stops mid-sentence.
func listItems(text string) ([]item, int) {
	var items []item
	cue := -1  // the last line announcing a plan
	open := -1 // the entry a continuation line belongs to, if any
	fenced := false

	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		body := strings.TrimLeft(line, " \t")
		indent := len(line) - len(body)

		// A list inside a code block is sample text, not a plan.
		if strings.HasPrefix(body, "```") || strings.HasPrefix(body, "~~~") {
			fenced = !fenced
			open = -1
			continue
		}
		if fenced || body == "" || isDecoration(body) {
			open = -1
			continue
		}
		if entry, ok := listItem(body); ok {
			items = append(items, item{indent: indent, line: n, text: entry})
			open = len(items) - 1
			continue
		}
		if isPlanCue(body) {
			cue = n
			open = -1
			continue
		}
		// Text indented under the entry above it is the rest of that entry, but
		// only while the join still reads as a task. isTask throws away
		// anything past maxTaskRunes whole, so a continuation that tips an
		// entry over the cap would cost the plan a real job rather than the
		// tail of one. Stop at the last row that fits and close the entry.
		if open >= 0 && indent >= items[open].indent+2 {
			joined := items[open].text + " " + body
			if utf8.RuneCountInString(joined) <= maxTaskRunes {
				items[open].text = joined
				continue
			}
		}
		open = -1
	}
	return items, cue
}

// planHeadings are the whole line, once its markup is stripped: a heading over
// the list that follows.
var planHeadings = []string{
	"plan", "the plan", "tasks", "the tasks", "work", "the work",
	"next steps", "steps", "proposed work", "what i would do", "what i'd do",
}

// planPhrases announce a plan in the middle of a sentence, so they are looked
// for anywhere in the line. Each is long enough not to appear by accident.
var planPhrases = []string{
	// "the plan is" is deliberately absent: it also ends sentences like "let me
	// read this first, so the plan is concrete", which announces nothing.
	"here's the plan", "here is the plan",
	"here's what i'd do", "here's what i would do",
	"i'll do the following", "i will do the following",
	"split this into", "fan this out",
}

// isPlanCue reports whether a line says that what follows is the work.
func isPlanCue(line string) bool {
	lower := strings.ToLower(tidyTask(line))
	for _, phrase := range planPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	heading := strings.TrimSpace(strings.Trim(lower, "#*_:. "))
	for _, h := range planHeadings {
		if heading == h {
			return true
		}
	}
	return false
}

// tidyTask turns an extracted entry into the prompt an agent would be given:
// one line, and without the markdown emphasis that only meant anything while it
// was being rendered.
func tidyTask(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	return strings.Join(strings.Fields(s), " ")
}

// isTask reports whether a line reads as work rather than as the interface drawn
// around it.
func isTask(s string) bool {
	r := []rune(s)
	if len(r) < minTaskRunes || len(r) > maxTaskRunes {
		return false
	}
	// A task is a phrase. One word is a spinner frame or the tail of a row that
	// was wrapped away from its own beginning.
	if len(strings.Fields(s)) < 2 {
		return false
	}
	// Something that opens on punctuation is the middle of a line, not a task.
	switch r[0] {
	case '`', '"', '\'':
	default:
		if !unicode.IsLetter(r[0]) && !unicode.IsDigit(r[0]) {
			return false
		}
	}
	// A question is something to answer, not something to go and do.
	if strings.HasSuffix(s, "?") {
		return false
	}
	lower := strings.ToLower(s)
	for _, phrase := range terminalChrome {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	for _, prefix := range terminalChromeOpeners {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	// An agent bullets its findings as readily as its plan. A finding opens by
	// naming the thing it is about — "The Go process owns the panes" — where an
	// instruction opens with the doing, or with what is to be done to. Note
	// that "I'll add …" and "We should …" are instructions, and stay.
	for _, opener := range []string{"the ", "there ", "this ", "that ", "these ", "those ", "it "} {
		if strings.HasPrefix(lower, opener) {
			return false
		}
	}
	return !isProgress(s)
}

// terminalChrome is what Claude Code's interface draws around what its agent
// says: the status line, the key hints, the token counters. A fan-out reading a
// pane's screen rather than its transcript sees all of it, and a status line
// beginning with a bullet glyph looks exactly like a list item.
var terminalChrome = []string{
	"esc to interrupt",
	"for shortcuts",
	"tokens ·",
	"tokens)",
	"thought for",
	"expand thinking",
	"context left",
	"auto-accept",
	"bypass permissions",
}

// terminalChromeOpeners is chrome that fills the line it opens, so it is only
// looked for at the start. A key hint stands alone on its own row, where the
// same text inside a sentence — "add a ctrl+k command palette" — is a job to
// do, and this application's plans are full of them.
var terminalChromeOpeners = []string{
	"ctrl+",
	"shift+tab",
}

// isProgress reports whether a line is Claude Code's status line: a word, an
// ellipsis and an elapsed time, as in "Perambulating… (26s".
func isProgress(s string) bool {
	i := strings.Index(s, "… (")
	if i < 0 {
		return false
	}
	rest := s[i+len("… ("):]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < len(rest) && rest[digits] == 's'
}

// listItem strips a leading list marker, reporting whether there was one.
//
// The glyphs Claude Code spins through while it works — · ▸ → among them — are
// deliberately not markers. On screen they sit at the head of a line with a
// space after them, which is indistinguishable from a bullet.
func listItem(line string) (string, bool) {
	// Bullets, and the checkbox one may carry.
	for _, marker := range []string{"* ", "- ", "• "} {
		if strings.HasPrefix(line, marker) {
			return trimCheckbox(strings.TrimPrefix(line, marker)), true
		}
	}
	// "1. text", "2) text", "10 - text"
	digits := 0
	for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits <= 3 && digits < len(line) {
		// The separator is allowed one leading space, which is how a dash is
		// written after a number: "10 - text" rather than "10- text".
		rest := strings.TrimPrefix(line[digits:], " ")
		for _, sep := range []string{". ", ") ", "- ", ": "} {
			if strings.HasPrefix(rest, sep) {
				return rest[len(sep):], true
			}
		}
	}
	// "TODO: text"
	for _, prefix := range []string{"TODO: ", "TODO ", "Task: "} {
		if strings.HasPrefix(line, prefix) {
			return line[len(prefix):], true
		}
	}
	return "", false
}

// trimCheckbox drops the box a task-list bullet carries: "[ ]", "[x]", and the
// "[X]" and "[-]" that get written just as often. Leaving it on would make the
// entry open on punctuation, which isTask reads as the middle of a line and
// throws away — so an unticked plan lost every item it had.
func trimCheckbox(s string) string {
	if len(s) >= 4 && s[0] == '[' && s[2] == ']' && s[3] == ' ' {
		return s[4:]
	}
	return s
}

// isDecoration reports whether a line is terminal furniture rather than words.
func isDecoration(line string) bool {
	letters := 0
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
		}
	}
	return letters*3 < len([]rune(line))
}

// SpawnOptions describes a child agent to start.
type SpawnOptions struct {
	// Task is given to the agent as its opening prompt.
	Task string
	// Cwd is where it works. Empty means the parent's directory.
	Cwd string
	// Branch, when set, puts the child in its own git worktree on that branch
	// so parallel children cannot collide over one checkout.
	Branch string
	// Split places the child beside its parent instead of in a new tab.
	Split bool
	Kind  session.Kind
	Title string
}

// Spawn starts a child agent, optionally in a worktree of its own.
//
// The task is handed to Claude as its opening argument rather than typed into
// the terminal: typing into a TUI means guessing when it is ready, while an
// argument is submitted by the agent itself the moment it starts.
func (w *Workspace) Spawn(parentPaneID string, o SpawnOptions) (string, error) {
	if strings.TrimSpace(o.Task) == "" && o.Kind != session.KindShell {
		return "", fmt.Errorf("a task is required")
	}
	if o.Kind == session.KindClaude && !w.ClaudeAvailable() {
		return "", fmt.Errorf("the `claude` CLI was not found on PATH")
	}

	parent := w.Pane(parentPaneID)
	cwd := o.Cwd
	if cwd == "" && parent != nil {
		cwd = parent.Cwd
	}
	if cwd == "" {
		cwd = w.activeRoot
	}

	if o.Branch != "" {
		path, err := w.worktreeFor(cwd, o.Branch)
		if err != nil {
			return "", err
		}
		cwd = path
	}

	title := o.Title
	if title == "" {
		title = summarisePrompt(o.Task)
	}
	if title == "" {
		title = filepath.Base(cwd)
	}

	p := &Pane{
		ID:      uuid.NewString(),
		Kind:    o.Kind,
		Cwd:     cwd,
		Name:    filepath.Base(cwd),
		Branch:  branchOf(cwd),
		initial: o.Task,
		Task:    o.Task,
	}
	w.mu.Lock()
	w.panes[p.ID] = p
	w.mu.Unlock()
	w.startPane(p, false)
	if p.Err != nil {
		err := p.Err
		w.destroyPane(p.ID)
		return "", err
	}

	// Place it: beside its parent, or as a tab of its own.
	placed := false
	if o.Split && parent != nil {
		for _, t := range w.Tabs {
			if t.Tree.Find(parentPaneID) == nil {
				continue
			}
			if t.Tree.Split(parentPaneID, p.ID, layout.Vertical) {
				t.Zoom = false
				placed = true
			}
			break
		}
	}
	if !placed {
		root := w.activeRoot
		if parent != nil {
			for _, t := range w.Tabs {
				if t.Tree.Find(parentPaneID) != nil {
					root = t.Root
					break
				}
			}
		}
		t := &Tab{
			ID:    uuid.NewString(),
			Root:  root,
			Title: title,
			Tree:  layout.NewLeaf(p.ID),
			Focus: p.ID,
		}
		w.Tabs = append(w.Tabs, t)
	}
	w.wake()
	return p.ID, nil
}

// PrepareWorktree creates (or reuses) a worktree for a branch and returns its
// path. It only runs git, so it is safe to call away from the goroutine that
// owns the workspace — which matters, because git is slow.
func (w *Workspace) PrepareWorktree(cwd, branch string) (string, error) {
	return w.worktreeFor(cwd, branch)
}

// worktreeFor creates (or reuses) a worktree for a branch and returns its path.
func (w *Workspace) worktreeFor(cwd, branch string) (string, error) {
	if !gitx.Available() {
		return "", fmt.Errorf("git is not installed, so a worktree cannot be created")
	}
	repo, err := gitx.Root(cwd)
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository", cwd)
	}
	// A branch that already has a worktree is reused rather than duplicated.
	if wts, err := gitx.List(repo); err == nil {
		for _, wt := range wts {
			if wt.Branch == branch {
				return wt.Path, nil
			}
		}
	}
	path := gitx.DefaultWorktreePath(repo, branch)
	if err := gitx.AddFrom(repo, path, branch, ""); err != nil {
		return "", err
	}
	return path, nil
}

// FanOut starts one agent per task.
//
// It returns the panes it created and, separately, what went wrong for the
// tasks it could not start: a worktree that could not be created for one task
// should not silently cancel the rest.
func (w *Workspace) FanOut(parentPaneID string, tasks []string, o SpawnOptions) ([]string, []error) {
	var (
		made []string
		errs []error
	)
	// The cap counts the tasks actually attempted, not positions in the slice.
	// The list arrives from a user who has been editing it, so it carries blank
	// rows, and counting those would let a handful of empty lines stand in for
	// agents that were never started.
	attempted := 0
	// Branch names are derived from the task text and then truncated, so two
	// tasks that begin alike derive the same name — and worktreeFor reuses the
	// worktree a branch already has, which would quietly put two agents in one
	// checkout. Keep the names each fan-out hands out distinct.
	used := map[string]bool{}
	for _, task := range tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		if attempted >= maxTasks {
			errs = append(errs, fmt.Errorf("stopped after %d tasks", maxTasks))
			break
		}
		attempted++
		opts := o
		opts.Task = task
		opts.Title = summarisePrompt(task)
		if o.Branch == autoBranch {
			opts.Branch = distinctBranch(BranchNameFor(task), used)
			used[opts.Branch] = true
		}
		id, err := w.Spawn(parentPaneID, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", opts.Title, err))
			continue
		}
		made = append(made, id)
	}
	return made, errs
}

// distinctBranch returns base, or base-2, base-3 and so on, until it names a
// branch this fan-out has not already handed to a sibling.
func distinctBranch(base string, used map[string]bool) string {
	candidate := base
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

// autoBranch asks FanOut to derive a branch name per task.
const autoBranch = "@auto"

// AutoBranch is the marker callers pass to give every child its own worktree
// on a branch named after its task.
const AutoBranch = autoBranch

// BranchNameFor derives a git branch name from a task description.
func BranchNameFor(task string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(task) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 32 {
			break
		}
	}
	name := strings.Trim(b.String(), "-")
	// Truncation lands mid-word as often as not; back up to the last word
	// boundary so the branch reads as words rather than a fragment.
	if len(name) >= 32 {
		if i := strings.LastIndex(name, "-"); i > 8 {
			name = name[:i]
		}
	}
	if name == "" {
		name = "task"
	}
	return "agent/" + name
}

// planTurns is how far back a fan-out looks for a plan. What the agent last
// said is usually it, but a plan followed by a short "done, shall I?" should
// still be found.
const planTurns = 4

// PlanSource is where a fan-out looks for the work it should propose.
type PlanSource struct {
	// SessionID names the pane's Claude transcript, when it has one.
	SessionID string
	// Screen is the pane's recent terminal output. It is the fallback: a shell
	// pane has no transcript, and neither has an agent that has not yet spoken.
	Screen string
}

// PlanSourceFor gathers what a fan-out can read from a pane.
//
// It only touches state already in memory, so it is safe on the goroutine that
// owns the workspace. Opening the transcript is Tasks's job.
func (w *Workspace) PlanSourceFor(paneID string) PlanSource {
	p := w.Pane(paneID)
	if p == nil {
		return PlanSource{}
	}
	var src PlanSource
	// A pane's id is the session id its agent was started with, which is what
	// names its transcript.
	if p.Kind == session.KindClaude {
		src.SessionID = p.ID
	}
	if p.Sess != nil {
		src.Screen = p.Sess.RecentText(recentOutputBytes)
	}
	return src
}

// Tasks proposes the work items in the plan, and reports whether they came from
// the agent's own words rather than from the screen.
//
// It reads from disk, so call it away from the goroutine that owns the
// workspace. The most recent reply that yields any work wins: an agent that has
// laid out a plan and then answered a follow-up should not lose the plan.
func (s PlanSource) Tasks() ([]string, bool) {
	for _, reply := range session.RecentReplies(s.SessionID, planTurns) {
		if tasks := ExtractTasks(reply); len(tasks) > 0 {
			return tasks, true
		}
	}
	return ExtractTasks(s.Screen), false
}
