package workspace

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/session"
)

// siblingTaskLimit and ownTaskLimit bound how much of a task is written out.
//
// What another agent is doing is orientation, and a line of it is enough. What
// this agent was asked is not: after a compaction this is the only copy of it
// left, and 160 characters of a longer instruction is worse than none, since
// what survives reads like the whole of it. The cap that remains is there to
// keep a pasted wall of text from crowding out the rest of the context.
const (
	siblingTaskLimit = 160
	ownTaskLimit     = 1200
)

// maxSiblings bounds how many other agents are named in a pane's context. A
// long list stops being orientation and starts being noise, and it is stale
// the moment it is read anyway.
const maxSiblings = 10

// Sibling is another pane, as described to an agent.
type Sibling struct {
	Name   string
	Tab    string
	Cwd    string
	Branch string
	Status string
	Shell  bool
	Task   string
	// Agent and Model are what the pane is running: the agent's id and the
	// model it was asked for, as the pane header shows them. Which agent is in
	// the next pane changes what is worth asking of it — a plan is worth
	// handing to one, a mechanical edit to another — and neither is anything
	// the reader can work out from a directory and a branch.
	Agent string
	Model string
	// SameCheckout marks a pane working in the reader's own directory. It is
	// the one fact in the list that changes what the agent should do, and it
	// is easy to miss when it has to be read off two long paths.
	SameCheckout bool
}

// PaneContext is everything an agent is told about the situation it is running
// in: which pane it is, which checkout it has, and who else is working.
//
// It is a snapshot taken on the workspace's own goroutine and rendered
// elsewhere, so it holds values rather than pointers into live state.
type PaneContext struct {
	PaneID   string
	PaneName string
	Shell    bool

	Tab      string
	TabPanes int

	ProjectRoot string
	ProjectName string
	Cwd         string
	Branch      string
	// Worktree reports that the pane works in a checkout of its own — a
	// directory outside the project altogether, which is what fan-out gives
	// each child, since Flockdeck puts a worktree beside the repository it came
	// from rather than inside it.
	Worktree bool
	// Subdirectory reports a working directory below the project root: the
	// same checkout, a directory or two down. It is not a worktree, and an
	// agent told that it is one is told to keep out of the rest of its own
	// repository.
	Subdirectory bool

	// Task is the opening prompt the pane was spawned with, when it was
	// started by a fan-out or by another agent rather than by hand. Which of
	// the two it was is not recorded, so the pane is not told either.
	Task string

	Siblings []Sibling
	// SiblingsOmitted counts the live panes left out of Siblings once the list
	// hit its limit, so the agent is told the list is partial rather than
	// assuming it has seen everyone.
	SiblingsOmitted int

	OtherProjects []string

	// Taken is when the snapshot was made. It is written out only for an agent
	// briefed through its opening prompt, because that briefing arrives once
	// and is never replaced: everything below it is true of the moment the
	// pane started and of no moment after, and an agent that is not told so
	// will read a list of other agents that has since changed as current.
	Taken time.Time

	// CanSpawn reports whether spawning helpers will work from this pane.
	CanSpawn bool
	// SpawnCommand is how Flockdeck itself is run from inside the pane: the name
	// on PATH where there is one, and the binary's own path where there is not.
	SpawnCommand string
}

// PaneContext describes the situation a pane is running in. It reports false
// when the pane is unknown.
//
// Everything here comes from state the workspace already holds — no git or
// filesystem calls — because it is built while a pane's Claude session waits
// on its SessionStart hook.
func (w *Workspace) PaneContext(paneID string) (PaneContext, bool) {
	p := w.Pane(paneID)
	if p == nil {
		return PaneContext{}, false
	}

	c := PaneContext{
		PaneID:       p.ID,
		PaneName:     p.Name,
		Shell:        p.Kind == session.KindShell,
		Cwd:          p.Cwd,
		Branch:       p.Branch,
		Task:         p.Task,
		Taken:        time.Now(),
		CanSpawn:     w.hookSrv != nil,
		SpawnCommand: w.spawnCmd,
	}

	own := w.tabOf(paneID)
	if own != nil {
		c.Tab = own.Title
		c.TabPanes = len(own.Tree.Panes())
	}
	// The pane's own project, not the project of the tab it is drawn on. A tab
	// can show agents from more than one project, and a borrowed pane told it
	// belongs to the tab's project is told the wrong root — which it would then
	// read as a worktree of a project it has nothing to do with.
	c.ProjectRoot = w.rootOf(paneID)
	// Projects are named the way the switcher names them, so an agent and the
	// user looking at it call the same thing by the same word — and so two
	// checkouts of one repository are not both simply "the project".
	names := projectNames(w.openRoots)
	c.ProjectName = filepath.Base(c.ProjectRoot)
	for i, root := range w.openRoots {
		if sameDir(root, c.ProjectRoot) {
			c.ProjectName = names[i]
			break
		}
	}
	if c.ProjectRoot != "" && !sameDir(c.Cwd, c.ProjectRoot) {
		c.Subdirectory = underDir(c.Cwd, c.ProjectRoot)
		c.Worktree = !c.Subdirectory
	}

	// The list is cut short in a busy project, so what it keeps matters more
	// than the order it keeps it in. An agent working in the reader's own
	// checkout comes first however far away it is drawn: it is the one fact
	// here that changes what the reader should do, and losing it to the cap
	// tells an agent it has its files to itself when it has not. Panes sharing
	// the tab come next, being the ones the user is watching side by side.
	var ownCheckoutSameTab, ownCheckout, sameTab, elsewhere []Sibling
	for _, t := range w.Tabs {
		for _, id := range t.Tree.Panes() {
			if id == paneID {
				continue
			}
			sib := w.Pane(id)
			if sib == nil {
				continue
			}
			// Membership follows the pane rather than the tab, for the same
			// reason the reader's own project does: an agent borrowed by
			// another project's tab is still working in this one, and one
			// borrowed from elsewhere is not.
			sibRoot := sib.Root
			if sibRoot == "" {
				sibRoot = t.Root
			}
			if !sameDir(sibRoot, c.ProjectRoot) {
				continue
			}
			st, _ := sib.Status()
			if st == session.StatusExited {
				continue
			}
			sibAgent, sibModel := paneAgent(sib)
			s := Sibling{
				Name:         sib.Name,
				Tab:          t.Title,
				Cwd:          sib.Cwd,
				Branch:       sib.Branch,
				Status:       st.String(),
				Shell:        sib.Kind == session.KindShell,
				Task:         sib.Task,
				Agent:        sibAgent,
				Model:        sibModel,
				SameCheckout: sameDir(sib.Cwd, c.Cwd),
			}
			switch onOwnTab := own != nil && t.ID == own.ID; {
			case s.SameCheckout && onOwnTab:
				ownCheckoutSameTab = append(ownCheckoutSameTab, s)
			case s.SameCheckout:
				ownCheckout = append(ownCheckout, s)
			case onOwnTab:
				sameTab = append(sameTab, s)
			default:
				elsewhere = append(elsewhere, s)
			}
		}
	}
	for _, group := range [][]Sibling{ownCheckoutSameTab, ownCheckout, sameTab, elsewhere} {
		c.Siblings = append(c.Siblings, group...)
	}
	if len(c.Siblings) > maxSiblings {
		c.SiblingsOmitted = len(c.Siblings) - maxSiblings
		c.Siblings = c.Siblings[:maxSiblings]
	}

	for i, root := range w.openRoots {
		if !sameDir(root, c.ProjectRoot) {
			c.OtherProjects = append(c.OtherProjects, names[i])
		}
	}
	sort.Strings(c.OtherProjects)

	return c, true
}

// OpeningPrompt is what the agent in a pane should be started with: the task
// it was given, with the briefing in front of it when the agent has no hooks
// to answer.
//
// Starting a pane calls this instead of passing the task straight through,
// because the briefing is the same one the hook returns and there is nowhere
// else for it to go for an agent that cannot be asked. Any other mode gets the
// task untouched: an agent with hooks is briefed when it fires its first one,
// and one asking for no context has said so.
//
// It is called on the goroutine that owns the workspace, like every other read
// of it.
func (w *Workspace) OpeningPrompt(paneID, task string, mode agent.ContextMode) string {
	if mode != agent.ContextPrompt {
		return task
	}
	c, ok := w.PaneContext(paneID)
	if !ok {
		// The pane is not in the workspace yet or is already gone. Either way
		// the agent still has work to do, and a task with no briefing is far
		// better than a briefing with no task.
		return task
	}
	// The task as the caller has it, rather than as the pane recorded it: a
	// pane started by hand has no Task at all, and one restarted has the task
	// it was spawned with long after the prompt it is being given now.
	c.Task = task
	return c.OpeningPrompt()
}

// paneAgent names the agent and the model a pane is running.
//
// It is a shim, and a temporary one: the two values belong on Pane, which is
// declared in a file this change does not own, so the briefing is written to
// read them from here and the body becomes `return p.Agent, p.Model` the
// moment the fields exist. Empty strings until then leave a sibling described
// exactly as it is described today.
func paneAgent(*Pane) (agentID, model string) { return "", "" }

// tabOf returns the tab containing a pane, or nil.
func (w *Workspace) tabOf(paneID string) *Tab {
	for _, t := range w.Tabs {
		if t.Tree.Find(paneID) != nil {
			return t
		}
	}
	return nil
}

// sameDir compares two directory paths for the purposes of telling a pane
// apart from its project root. Case is ignored, since Windows paths reach us
// from both the command line and git.
func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// underDir reports whether dir is base or sits inside it.
//
// Case is folded for the same reason sameDir folds it: the two paths arrive
// from different places — one from a project as it was opened, the other from
// git or from a directory picker — and on Windows they can name the same
// directory in different case.
func underDir(dir, base string) bool {
	d, b := filepath.Clean(dir), filepath.Clean(base)
	if runtime.GOOS == "windows" {
		// Folded before the comparison, not after: filepath.Rel compares the
		// two case sensitively, so folding its answer would be too late.
		d, b = strings.ToLower(d), strings.ToLower(b)
	}
	rel, err := filepath.Rel(b, d)
	if err != nil {
		return false
	}
	// Only a leading path element of ".." means the way out; a directory
	// genuinely named something like "..old" is inside the tree it sits in.
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Render writes the context as the text handed to an agent at session start,
// in answer to its own lifecycle hook.
//
// It is prose rather than JSON because it is read by the model, and it is
// deliberately specific about the two things an agent cannot work out for
// itself: that its conversation is private to one pane among several, and that
// the other panes may be editing other checkouts of the same repository.
func (c PaneContext) Render() string { return c.render(false) }

// OpeningPrompt is what an agent with no hooks to answer is started with: the
// same briefing, fenced, and then the task.
//
// The fence is there so the two can be told apart. Everything inside it is
// Flockdeck describing the pane; everything after it is what the user asked for,
// and an agent that reads the first as part of the second sets about
// documenting the window instead of doing the work. A pane started with no
// task gets the block on its own, which is still worth sending: knowing which
// checkout it has and who else is in the repository changes what the agent
// does with the first thing the user types.
func (c PaneContext) OpeningPrompt() string {
	var b strings.Builder
	b.WriteString("<flockdeck-context>\n")
	b.WriteString(strings.TrimRight(c.render(true), "\n"))
	b.WriteString("\n</flockdeck-context>\n")
	// The task follows whole and unsummarised, which is why the block above
	// leaves out the line that repeats it back: a briefing that quotes the
	// first thousand characters of an instruction printed in full two lines
	// below is a second, shorter copy for the agent to disagree with.
	if task := strings.TrimSpace(c.Task); task != "" {
		b.WriteString("\n" + task + "\n")
	}
	return b.String()
}

// render writes the briefing. viaPrompt says it is going in front of an
// opening prompt rather than back down a lifecycle hook, which changes two
// things: the briefing has to admit that it will never be refreshed, and it
// must not describe a status Flockdeck reads from hooks the agent does not have.
func (c PaneContext) render(viaPrompt bool) string {
	var b strings.Builder

	b.WriteString("# Where you are running\n\n")
	fmt.Fprintf(&b, "You are one agent inside **Flockdeck**, a desktop application that runs "+
		"several coding agents side by side in terminal panes. You are the agent in the pane "+
		"named %q", c.PaneName)
	if c.Tab != "" {
		fmt.Fprintf(&b, ", in the tab %q", c.Tab)
	}
	if c.ProjectName != "" {
		fmt.Fprintf(&b, ", in the project %q", c.ProjectName)
	}
	b.WriteString(".\n\n")

	if viaPrompt {
		b.WriteString("Everything below was true when this pane started")
		if !c.Taken.IsZero() {
			fmt.Fprintf(&b, ", at %s", c.Taken.Format("2006-01-02 15:04 MST"))
		}
		b.WriteString(". You are briefed once, here: none of it is sent again or brought " +
			"up to date, so the other agents listed may since have finished, moved on, or been " +
			"joined by others.\n\n")
	}

	b.WriteString("## This pane\n\n")
	fmt.Fprintf(&b, "- Working directory: `%s`\n", c.Cwd)
	if c.Branch != "" {
		fmt.Fprintf(&b, "- Branch: `%s`\n", c.Branch)
	}
	if c.Worktree {
		fmt.Fprintf(&b, "- This is a separate git worktree, not the project root (`%s`). "+
			"Work here; do not edit files under another checkout.\n", c.ProjectRoot)
	}
	if c.Subdirectory {
		fmt.Fprintf(&b, "- The project root is `%s`; you are working in a directory "+
			"below it, in the same checkout.\n", c.ProjectRoot)
	}
	if c.TabPanes > 1 {
		fmt.Fprintf(&b, "- Your tab is split across %d panes; the user can see them all at once.\n", c.TabPanes)
	}
	if c.Task != "" && !viaPrompt {
		fmt.Fprintf(&b, "- You were started with this task: %s\n", oneLine(c.Task, ownTaskLimit))
	}
	b.WriteString("- Your conversation belongs to this pane alone. It is resumed when the pane is " +
		"restored, so what you say here outlives the window.\n")

	b.WriteString("\n## The other agents\n\n")
	if len(c.Siblings) == 0 {
		b.WriteString("No other pane is running in this project right now.\n")
	} else {
		b.WriteString("These panes are running alongside you in this project. " +
			"They are separate sessions: you cannot see their conversations and they cannot " +
			"see yours, so nothing is shared unless the user or a commit carries it across.\n\n")
		for _, s := range c.Siblings {
			b.WriteString("- " + s.describe() + "\n")
		}
		if c.SiblingsOmitted > 0 {
			fmt.Fprintf(&b, "- …and %d more, not listed.\n", c.SiblingsOmitted)
		}
		b.WriteString("\nIf more than one of you is working in the same checkout, expect files to " +
			"change under you and re-read before editing.\n")
		if c.siblingAgentsNamed() {
			// Flockdeck runs whichever coding agents the user has, and they are not
			// interchangeable. Where the pane says which one it is, the reader
			// can pitch what it asks of it — and can stop assuming the pane next
			// door works the way it does.
			b.WriteString("\nWhere an agent and a model are named, that is what the pane is " +
				"running. They differ in what they are good at and in what they can reach, so it " +
				"is worth knowing which one you would be asking.\n")
		}
	}
	if len(c.OtherProjects) > 0 {
		fmt.Fprintf(&b, "\nOther projects are open in the same window (%s); their agents are not "+
			"working on this one.\n", strings.Join(c.OtherProjects, ", "))
	}

	writeCapabilities(&b, !viaPrompt)

	if c.CanSpawn && !c.Shell {
		// The examples name the command that will actually run here. A copy
		// that has not been installed is not on PATH, and an agent given
		// `flockdeck spawn` in that case is given something that cannot work.
		flockdeck := shellWord(c.SpawnCommand)
		if flockdeck == "" {
			flockdeck = "flockdeck"
		}
		b.WriteString("\n## Starting agents of your own\n\n" +
			"You can hand work to further agents, which appear as panes of their own:\n\n" +
			"```sh\n" +
			flockdeck + " spawn \"add tests for the parser\"\n" +
			flockdeck + " spawn --worktree fix-auth \"repair the token refresh\"\n" +
			flockdeck + " spawn --split \"watch the build\"\n" +
			flockdeck + " spawn --split --shell \"tail the build log\"\n" +
			"```\n\n" +
			"What the flags do — the placement ones matter, because a pane put somewhere " +
			"the user did not expect is one they have to go looking for:\n\n" +
			"- No placement flag: the agent opens in a **new tab** of its own, titled after " +
			"the task you gave it.\n" +
			"- `--split`: the agent opens **beside you, in the tab you are in**. Splitting " +
			"again adds a sibling to that same split rather than nesting inside it, so " +
			"several `--split` calls gather the whole group in front of the user at once.\n" +
			"- `--worktree <branch>`: the agent gets a git worktree and branch of its own, " +
			"created from yours. Without it every agent shares this one working tree and " +
			"will edit files under the others; use it whenever two of them would otherwise " +
			"touch the same files.\n" +
			"- `--shell`: a terminal rather than another conversation.\n\n" +
			"Each one is a fresh agent with an empty conversation: it inherits nothing from " +
			"yours — not this context, not the task you were given, not what you have " +
			"learned so far — so the task you give it has to stand on its own. Do this when " +
			"the user asks for parallel work, not on your own initiative.\n")

		writeCommandLine(&b, flockdeck)
	}

	b.WriteString("\n## The user's view\n\n" +
		"The user watches every pane at once and is told which one is waiting on them, so a " +
		"question here is seen even when they are looking elsewhere. They may also type the " +
		"same instruction into several panes at once; if a message reads as though it were " +
		"meant for a different pane, answer for your own directory and branch.\n")

	return b.String()
}

// writeCapabilities describes the application the agent is running inside.
//
// An agent told only that it is in "Flockdeck" has been told the name of something
// it cannot use. Two kinds of thing are written here. The first is the
// behaviour that changes how an agent should work: a status the user is
// watching, a fan-out that reads the agent's own output, a commit button that
// takes the working tree whole. The second is the plain list of what the
// interface can do, so that a user who asks how to do something is answered by
// the agent in front of them rather than sent to the help.
//
// The shortcuts are not written out by hand. They come from the one table the
// command palette and the help pages are drawn from, so a rebinding cannot be
// made there and left stale here.
//
// hooked says the agent reports its own lifecycle to Flockdeck. Where it does not,
// the status paragraph has to describe the fallback instead: telling an agent
// that its state is read from hooks it never fires, out of a settings file it
// was never given, is telling it its questions are noticed when they may not
// be.
func writeCapabilities(b *strings.Builder, hooked bool) {
	b.WriteString("\n## What Flockdeck can do\n\n" +
		"Some of this changes how you should work; the rest is here so that a user who asks " +
		"how to do something gets an answer from you.\n\n")

	if hooked {
		fmt.Fprintf(b, "**Your status is watched, so stopping to ask is cheap.** Every agent pane "+
			"is started with a generated `--settings` file registering Claude Code's lifecycle "+
			"hooks — your own settings, hooks and permissions still apply on top — and Flockdeck reads "+
			"your state from those rather than from your output: green while you work, amber while "+
			"you wait on the user, grey between turns, red once the process exits. A pane that is "+
			"waiting marks its tab and the window title, and raises a desktop notification when the "+
			"window is not in front. The pane header names the tool you are running while it runs. "+
			"%s reaches any pane in any open project from anywhere.\n\n", how("agents"))
	} else {
		fmt.Fprintf(b, "**Your status is watched, so stopping to ask is worth it.** You report no "+
			"lifecycle events to Flockdeck, so it colours this pane from what it prints and how long it "+
			"has been quiet: green while you work, amber when what you last printed reads as a "+
			"question, grey between turns, red once the process exits. A pane that is waiting marks "+
			"its tab and the window title, and raises a desktop notification when the window is not "+
			"in front, so a question does reach the user even when they are looking elsewhere — but "+
			"it is read off your output rather than told to Flockdeck, so ask plainly, on a line of its "+
			"own, and wait for an answer rather than assuming one. %s reaches any pane in any open "+
			"project from anywhere.\n\n", how("agents"))
	}

	fmt.Fprintf(b, "**Fan out turns your own output into agents.** %s reads the list your last "+
		"message ends with, hands the user an editable copy of it, and starts one agent per "+
		"line — up to twelve, each in a git worktree and branch of its own, each given its line "+
		"as its opening prompt. A plan written as one self-contained task per line can be run "+
		"as it stands; a plan written as prose has to be rewritten before it can be, so write "+
		"one that way when it is going to be handed to other agents.\n\n", how("fanout"))

	fmt.Fprintf(b, "**Broadcast is one instruction to several panes.** %s mirrors what the user "+
		"types into a set of panes — by default the agents on the current tab, adjusted with "+
		"the `⇉` button in each pane header — and %s sends that set one composed message at "+
		"once.\n\n", how("toggleBroadcast"), how("promptAll"))

	fmt.Fprintf(b, "**Git has a home in the window.** %s shows the diff of the checkout this "+
		"pane is working in, and commits, pushes, pulls and fetches it. Nothing is staged "+
		"selectively there: a commit takes the working tree as it stands, so unrelated edits "+
		"left lying about go in with it. %s lists every worktree with its branch, the agents "+
		"working in it, its uncommitted files and how far it stands from upstream; it creates "+
		"one for a new branch, and opens an agent, a shell or a diff in any of them. Every pane "+
		"header carries the same for its own checkout.\n\n", how("changes"), how("worktrees"))

	fmt.Fprintf(b, "**A pane and its conversation are one thing.** %s starts the process again "+
		"in the same directory and resumes this conversation, which is the repair for an agent "+
		"that has wedged itself rather than a way to clear the screen; %s ends it. Layouts, "+
		"projects and conversations are restored on the next run, %s closes the window and "+
		"leaves every agent running, and %s reopens a stored transcript in a tab. Panes are "+
		"moved rather than recreated: dragged to another edge, another tab or a tab of their "+
		"own, nothing restarts and the conversation, directory and scrollback come with "+
		"them.\n\n", how("restartPane"), how("closePane"), how("detach"), how("history"))

	fmt.Fprintf(b, "**Several projects are open at once**, each with its own tabs, layout and "+
		"restored conversations. %s switches between them, which stops nothing: the other "+
		"project's agents keep working, and a pane can be split into another project while "+
		"staying on this tab.\n\n", how("projects"))

	fmt.Fprintf(b, "Every action there is, and the keys for it. The ones marked "+
		"(command palette) have no binding of their own; %s searches the whole list:\n\n",
		how("palette"))
	for _, section := range help.Sections {
		var parts []string
		for _, k := range help.InSection(section) {
			if k.Keys == "" {
				parts = append(parts, k.Name()+" (command palette)")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s `%s`", k.Name(), k.Keys))
		}
		if len(parts) == 0 {
			continue
		}
		fmt.Fprintf(b, "- **%s** — %s\n", section, strings.Join(parts, "; "))
	}
}

// writeCommandLine documents what the pane carries in its environment and what
// the rest of the command line does.
//
// `spawn` is the part of it meant for an agent, and it has a section of its
// own. This is here so that the rest is recognised rather than experimented
// with: `-quit` reads like a way to end this pane and is a way to end every
// agent in every project, and an agent that has never been told about
// `FLOCKDECK_PANE_NAME` cannot answer the user asking how to put it in a prompt.
func writeCommandLine(b *strings.Builder, flockdeck string) {
	fmt.Fprintf(b, "\n## Flockdeck from the shell\n\n"+
		"The rest of the command line operates the application itself. It reaches the "+
		"instance already running rather than starting a second one:\n\n"+
		"```sh\n"+
		"%s -C ~/code/api   # open another project in this window\n"+
		"%s -detach         # close the window, leave every agent running\n"+
		"%s -quit           # stop every agent in every project\n"+
		"```\n\n"+
		"Those are the user's to run rather than yours — `-quit` ends the other agents' work "+
		"along with your own. `-new`, `-shell`, `-solo`, `-no-window` and `-version` shape a "+
		"fresh start and mean nothing from in here, and `FLOCKDECK_BROWSER` picks the browser that "+
		"provides the window. The interface is a local page: Flockdeck serves it on `127.0.0.1` on "+
		"a random port, behind a token generated for each run, and exposes nothing to the "+
		"network.\n\n", flockdeck, flockdeck, flockdeck)

	b.WriteString("Your pane carries the rest in its environment:\n\n" +
		"| Variable | What it is |\n" +
		"| --- | --- |\n" +
		"| `FLOCKDECK_API` | where Flockdeck listens for its panes |\n" +
		"| `FLOCKDECK_TOKEN` | the secret that goes with it, which never leaves this pane |\n" +
		"| `FLOCKDECK_PANE` | this pane's id, which `spawn` sends so a helper is placed relative to you |\n" +
		"| `FLOCKDECK_PANE_NAME` | this pane's name |\n" +
		"| `FLOCKDECK_PROJECT` | the project directory this pane belongs to |\n\n" +
		"`spawn` reads the first three, which is why it works from inside a pane and nowhere " +
		"else. The same values are set under the older `PERCH_*` names as well, for " +
		"anything written before the application was renamed.\n")
}

// how names an action as prose calls it and says how it is reached, from the
// same table the palette and the help pages read.
func how(id string) string {
	k, ok := help.Lookup(id)
	if !ok {
		// An id that has been renamed out from under this text should read as
		// the gap it is, rather than quietly leaving a sentence describing a
		// feature with no way to reach it.
		return id
	}
	if k.Keys == "" {
		return k.Name() + " (command palette)"
	}
	return k.Name() + " (" + k.Keys + ")"
}

// describe renders one sibling as a single line.
//
// Panes are named after their directory, so several of them share a name; the
// tab is what the user actually calls them, and after a fan-out it is named
// after the task. It is included whenever it says something the name does not.
func (s Sibling) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q", s.Name)
	if s.Tab != "" && !strings.EqualFold(s.Tab, s.Name) {
		fmt.Fprintf(&b, " in the tab %q", s.Tab)
	}
	if s.Shell {
		b.WriteString(" (a shell, not an agent)")
	} else if label := agentLabel(s.Agent, s.Model); label != "" {
		fmt.Fprintf(&b, " running %s", label)
	}
	if s.Branch != "" {
		fmt.Fprintf(&b, " on branch `%s`", s.Branch)
	}
	if s.SameCheckout {
		// The path is the reader's own, so repeating it says nothing; that it
		// is shared says everything.
		b.WriteString(" in the same directory as you")
	} else {
		fmt.Fprintf(&b, " in `%s`", s.Cwd)
	}
	if !s.Shell {
		fmt.Fprintf(&b, " — %s", s.Status)
	}
	if s.Task != "" {
		fmt.Fprintf(&b, "; working on: %s", oneLine(s.Task, siblingTaskLimit))
	}
	return b.String()
}

// agentLabel names an agent and its model the way the pane header does —
// `claude · sonnet`, `codex · gpt-5` — so that the agent reading about a
// sibling and the user looking at it are told the same thing in the same
// words. A model the agent was not asked for is not invented: an empty one
// means whatever that CLI is configured with, which is not Flockdeck's to report.
func agentLabel(agentID, model string) string {
	if agentID == "" || model == "" {
		return agentID
	}
	return agentID + " · " + model
}

// siblingAgentsNamed reports whether any sibling says what it is running, so
// the note explaining the labels is written only where there are labels.
func (c PaneContext) siblingAgentsNamed() bool {
	for _, s := range c.Siblings {
		if !s.Shell && s.Agent != "" {
			return true
		}
	}
	return false
}

// shellWord quotes a command for the shell the examples are written for.
//
// The path to a build that has not been installed is full of characters a
// shell reads as syntax rather than as part of a name: the space in "Program
// Files", the brackets after it. Anything but what a bare path is made of is
// quoted, rather than the space alone.
func shellWord(s string) string {
	if s == "" || !strings.ContainsFunc(s, needsQuoting) {
		return s
	}
	return `"` + s + `"`
}

// needsQuoting reports a character a shell would not read as part of a path.
func needsQuoting(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '.', r == '_', r == '-', r == ':', r == '/', r == '\\':
		return false
	}
	return true
}

// oneLine flattens a prompt onto a single line and shortens it to max runes,
// so a pasted wall of text does not become the bulk of the context.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	// Counted in runes, not bytes: cutting a task written in any non-ASCII
	// script mid-rune would put a replacement character into the context.
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}
