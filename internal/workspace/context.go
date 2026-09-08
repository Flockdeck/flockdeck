package workspace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmwri/agent-wrapper/internal/session"
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
	// Worktree reports that the pane works in a checkout of its own rather
	// than in the project root, which is what fan-out gives each child.
	Worktree bool

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

	// CanSpawn reports whether `agent-wrapper spawn` will work from this pane.
	CanSpawn bool
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
		PaneID:   p.ID,
		PaneName: p.Name,
		Shell:    p.Kind == session.KindShell,
		Cwd:      p.Cwd,
		Branch:   p.Branch,
		Task:     p.Task,
		CanSpawn: w.hookSrv != nil,
	}

	// The tab the pane lives in also identifies its project: a fan-out child
	// in a worktree works well outside its project root.
	own := w.tabOf(paneID)
	if own != nil {
		c.Tab = own.Title
		c.TabPanes = len(own.Tree.Panes())
		c.ProjectRoot = own.Root
	}
	if c.ProjectRoot == "" {
		c.ProjectRoot = w.activeRoot
	}
	c.ProjectName = filepath.Base(c.ProjectRoot)
	c.Worktree = c.ProjectRoot != "" && !sameDir(c.Cwd, c.ProjectRoot)

	// Panes sharing the tab come first. They are the ones the agent is most
	// likely to collide with — a split tab usually means one checkout — and
	// the list is cut short in a busy project, so they must not be the ones
	// that get dropped.
	var sameTab, elsewhere []Sibling
	for _, t := range w.Tabs {
		if t.Root != c.ProjectRoot {
			continue
		}
		for _, id := range t.Tree.Panes() {
			if id == paneID {
				continue
			}
			sib := w.Pane(id)
			if sib == nil {
				continue
			}
			st, _ := sib.Status()
			if st == session.StatusExited {
				continue
			}
			s := Sibling{
				Name:         sib.Name,
				Tab:          t.Title,
				Cwd:          sib.Cwd,
				Branch:       sib.Branch,
				Status:       st.String(),
				Shell:        sib.Kind == session.KindShell,
				Task:         sib.Task,
				SameCheckout: sameDir(sib.Cwd, c.Cwd),
			}
			if own != nil && t.ID == own.ID {
				sameTab = append(sameTab, s)
				continue
			}
			elsewhere = append(elsewhere, s)
		}
	}
	c.Siblings = append(sameTab, elsewhere...)
	if len(c.Siblings) > maxSiblings {
		c.SiblingsOmitted = len(c.Siblings) - maxSiblings
		c.Siblings = c.Siblings[:maxSiblings]
	}

	// Named the way the project switcher names them, so two checkouts of one
	// repository do not both reach the agent as the same word.
	names := projectNames(w.openRoots)
	for i, root := range w.openRoots {
		if !sameDir(root, c.ProjectRoot) {
			c.OtherProjects = append(c.OtherProjects, names[i])
		}
	}
	sort.Strings(c.OtherProjects)

	return c, true
}

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

// Render writes the context as the text handed to Claude at session start.
//
// It is prose rather than JSON because it is read by the model, and it is
// deliberately specific about the two things an agent cannot work out for
// itself: that its conversation is private to one pane among several, and that
// the other panes may be editing other checkouts of the same repository.
func (c PaneContext) Render() string {
	var b strings.Builder

	b.WriteString("# Where you are running\n\n")
	fmt.Fprintf(&b, "You are one agent inside **agent-wrapper**, a desktop application that runs "+
		"several Claude Code agents side by side in terminal panes. You are the agent in the pane "+
		"named %q", c.PaneName)
	if c.Tab != "" {
		fmt.Fprintf(&b, ", in the tab %q", c.Tab)
	}
	if c.ProjectName != "" {
		fmt.Fprintf(&b, ", in the project %q", c.ProjectName)
	}
	b.WriteString(".\n\n")

	b.WriteString("## This pane\n\n")
	fmt.Fprintf(&b, "- Working directory: `%s`\n", c.Cwd)
	if c.Branch != "" {
		fmt.Fprintf(&b, "- Branch: `%s`\n", c.Branch)
	}
	if c.Worktree {
		fmt.Fprintf(&b, "- This is a separate git worktree, not the project root (`%s`). "+
			"Work here; do not edit files under another checkout.\n", c.ProjectRoot)
	}
	if c.TabPanes > 1 {
		fmt.Fprintf(&b, "- Your tab is split across %d panes; the user can see them all at once.\n", c.TabPanes)
	}
	if c.Task != "" {
		fmt.Fprintf(&b, "- You were started with this task: %s\n", oneLine(c.Task))
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
	}
	if len(c.OtherProjects) > 0 {
		fmt.Fprintf(&b, "\nOther projects are open in the same window (%s); their agents are not "+
			"working on this one.\n", strings.Join(c.OtherProjects, ", "))
	}

	if c.CanSpawn && !c.Shell {
		b.WriteString("\n## Starting agents of your own\n\n" +
			"You can hand work to further agents, which appear as panes beside you:\n\n" +
			"```sh\n" +
			"agent-wrapper spawn \"add tests for the parser\"\n" +
			"agent-wrapper spawn --worktree fix-auth \"repair the token refresh\"\n" +
			"agent-wrapper spawn --split \"watch the build\"\n" +
			"agent-wrapper spawn --split --shell \"tail the build log\"\n" +
			"```\n\n" +
			"Each one is a fresh agent with an empty conversation: it inherits nothing from " +
			"yours, so the task you give it has to stand on its own. Use `--worktree` when two " +
			"of them would otherwise edit the same files, and `--shell` when what you want " +
			"beside you is a terminal rather than another conversation. Do this when the " +
			"user asks for parallel work, not on your own initiative.\n")
	}

	b.WriteString("\n## The user's view\n\n" +
		"The user watches every pane at once and is told which one is waiting on them, so a " +
		"question here is seen even when they are looking elsewhere. They may also type the " +
		"same instruction into several panes at once; if a message reads as though it were " +
		"meant for a different pane, answer for your own directory and branch.\n")

	return b.String()
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
		fmt.Fprintf(&b, "; working on: %s", oneLine(s.Task))
	}
	return b.String()
}

// oneLine flattens a prompt onto a single line and shortens it, so a pasted
// wall of text does not become the bulk of the context.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 160
	// Counted in runes, not bytes: cutting a task written in any non-ASCII
	// script mid-rune would put a replacement character into the context.
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}
