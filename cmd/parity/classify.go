package main

// Kind is what is said of an action or command the new interface does not do
// yet.
type Kind int

const (
	// Cut was dropped on purpose. Why says why.
	Cut Kind = iota + 1
	// Replaced is done by another command the new interface sends. To names it.
	Replaced
	// Planned is owned by a task on the board. To is the task, Why what it covers.
	Planned
)

// Verdict is one classification. A thing the new interface does is not listed
// here: the sources say so.
type Verdict struct {
	Kind Kind
	To   string
	Why  string
}

// describe is the status and note a table shows.
func (v Verdict) describe() (status, note string) {
	switch v.Kind {
	case Cut:
		return "cut", v.Why
	case Replaced:
		s := "sent as `" + v.To + "`"
		if v.Why != "" {
			s += ". " + v.Why
		}
		return "replaced", s
	default:
		s := v.To
		if v.Why != "" {
			s += ". " + v.Why
		}
		return "planned", s
	}
}

func cut(why string) Verdict          { return Verdict{Kind: Cut, Why: why} }
func replaced(to, why string) Verdict { return Verdict{Kind: Replaced, To: to, Why: why} }
func task(id string, why ...string) Verdict {
	v := Verdict{Kind: Planned, To: id}
	if len(why) > 0 {
		v.Why = why[0]
	}
	return v
}

// The tasks of the team rebuild's stage 3 onwards, by the board's ids. The plan
// is ide-stage-3-plus-tasks.md; the names are the titles on the board.
const (
	tC5 = "T131 (C5, task detail)"
	tC6 = "T132 (C6, Changes)"
	tC7 = "T133 (C7, worktrees)"
	tC8 = "T134 (C8, GitHub)"
	tL1 = "T136 (L1, start flows and pane lifecycle)"
	tL2 = "T137 (L2, settings)"
	tL3 = "T138 (L3, help)"
	tL6 = "T141 (L6, helpers, baton, projects)"
	tL8 = "T143 (L8, remaining actions)"
)

const (
	whyTiling    = "Tiling and splitting panes is cut; the team is a graph."
	whyRearrange = "Moving and merging panes and tabs is cut; the team is a graph."
	whyBroadcast = "Broadcasting to panes is cut; a manager sends to its team."
)

// keyVerdicts classify every action of the key table that the new shell does not
// run. An action it runs must not be in this list as cut.
var keyVerdicts = map[string]Verdict{
	// Panes.
	"splitRight":       cut(whyTiling),
	"splitDown":        cut(whyTiling),
	"splitRightChoose": cut(whyTiling + " Starting an agent with a chosen agent is the start flow."),
	"splitRightShell":  cut(whyTiling + " Starting a shell is the start flow."),
	"movePaneLeft":     cut(whyRearrange),
	"movePaneRight":    cut(whyRearrange),
	"movePaneUp":       cut(whyRearrange),
	"movePaneDown":     cut(whyRearrange),
	"movePaneToNewTab": cut(whyRearrange),
	"tilePanes":        cut(whyTiling),

	// Tabs.
	"selectTab":    task(tL1, "The tab strip sends selectTab when a tab is pressed; the Alt+number key is not run"),
	"mergeAllTabs": cut(whyRearrange),

	// Agents.
	"toggleBroadcast":       cut(whyBroadcast),
	"toggleBroadcastMember": cut(whyBroadcast),
	"promptAll":             cut(whyBroadcast + " It opens the prompt bar aimed at the focused pane, which the new prompt (paneSend) covers; with broadcast on it reached every member."),
	"closeFinishedPanes":    task(tL1),

	// Git.
	"changes": task(tC6),
}

// cmdVerdicts classify every control-socket command the old interface sends
// that the new one does not. The new interface's own commands are not here.
var cmdVerdicts = map[string]Verdict{
	// Replaced by what the new interface sends.
	"sendPrompt": replaced("paneSend", "Prompting one pane; prompting all is cut"),

	// Cut.
	"splitPane":             cut(whyTiling),
	"swapPanes":             cut(whyRearrange),
	"movePane":              cut(whyRearrange),
	"movePaneDir":           cut(whyRearrange),
	"movePaneToNewTab":      cut(whyRearrange),
	"movePaneToTab":         cut(whyRearrange),
	"tilePanes":             cut(whyTiling),
	"mergeTab":              cut(whyRearrange),
	"mergeAllTabs":          cut(whyRearrange),
	"toggleBroadcast":       cut(whyBroadcast),
	"toggleBroadcastMember": cut(whyBroadcast),

	// L1: start flows and pane lifecycle.
	"closeFinishedPanes": task(tL1),
	"toggleZoom":         task(tL1),
	"focusPane":          task(tL1),
	"refreshAgents":      task(tL1, "The agent picker"),
	"teamClose":          task(tL1),

	// L2: settings.
	"statusLine":       task(tL2),
	"railExpanded":     task(tL2),
	"railWidth":        task(tL2),
	"setKeybinding":    task(tL2),
	"resetKeybinding":  task(tL2),
	"resetKeybindings": task(tL2),
	"dismissTip":       task(tL2),
	"setAgentDefault":  task(tL2),
	"setAgentAddress":  task(tL2),

	// L3: help.
	"helpSeen": task(tL3, "The first-run welcome"),

	// L7: updates, detach, quit.

	// Git, worktrees, GitHub.
	"changes":        task(tC6),
	"commit":         task(tC6),
	"diff":           task(tC6),
	"gitFetch":       task(tC6),
	"gitPull":        task(tC6),
	"gitPush":        task(tC6),
	"ghChecks":       task(tC8),
	"ghInstall":      task(tC8),
	"ghIssue":        task(tC8),
	"ghIssueComment": task(tC8),
	"ghIssueCreate":  task(tC8),
	"ghIssues":       task(tC8),
	"ghLogin":        task(tC8),
	"ghLoginCancel":  task(tC8),
	"ghLogout":       task(tC8),
	"ghPR":           task(tC8),
	"ghPRComment":    task(tC8),
	"ghPRCreate":     task(tC8),
	"ghPRs":          task(tC8),
	"ghStatus":       task(tC8),

	// The widths of tiled panes.
	"setWeights": cut(whyTiling),
}

// dynamicSites are the places the old interface builds the name of a command at
// run time, as the scan reads them. The names each can take are scanned from the
// site (a ternary) or from PREF_CMD; msg.resend takes the name of a command the
// desktop asks to be sent again, which is one of the git commands already
// classified. A site not listed here fails the test, so a new one is looked at.
var dynamicSites = []string{
	`PREF_CMD[field]`,
	`isOpen ? "renameGroup" : "renameProject"`,
	`kind === "prs" ? "ghPR" : "ghIssue"`,
	`msg.resend`,
	`sel.kind === "prs" ? "ghPRComment" : "ghIssueComment"`,
}

// helperCommands are commands the old interface sends through a local helper
// that builds the message from a variable (run("saveBaton")), so the scan of
// `cmd: "name"` cannot see them.
var helperCommands = []string{"saveBaton", "restartWithBaton", "startFromBaton"}

// serverSuppliedCommands are commands the old interface sends because the
// desktop's own message names them (a notice's action), so no source of the old
// interface holds the name.
var serverSuppliedCommands = []string{"approveBaton"}

// notCommands are strings that are a `case` of a switch in internal/server and
// are also quoted in the old interface, without being commands it sends: message
// types, tool names, option values. The test of the other direction (a command
// of the server that the old interface names, and the scan missed) skips these.
var notCommands = []string{
	"Bash", "Edit", "MultiEdit", "Write", "all", "auto", "block", "clear", "close", "dark",
	"down", "hello", "left", "list", "open", "recordings", "resize", "set", "teal", "top", "up",
}

// blocksRetirement are commands that are safety or security controls. The new
// interface has to have them before the old one is removed (T162), whichever
// order the rest are built in.
// When the new interface sends one of these commands the row loses its task and
// TestRetirementGateIsInTheDoc fails: that is the cue to remove it from this list.
// autoReviewDefault left it that way: it is the default a new pane starts with,
// not a control on a running pane, and junk in the file reads as off. autoReview
// (a running pane's own switch) and jevKey have both been built, so the list is empty.
var blocksRetirement = []string{}
