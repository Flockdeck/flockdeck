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
	tL4 = "T139 (L4, history, find, pane info)"
	tL5 = "T140 (L5, API keys, remote, notifications)"
	tL6 = "T141 (L6, helpers, baton, projects)"
	tL7 = "T142 (L7, updates, detach, quit)"
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
	"zoomPane":         task(tL1),
	"restartPane":      task(tL1),
	"lockPane":         task(tL1),
	"recordPane":       task(tL4, "Recording a terminal pane; recording a structured pane is T145 (P1)"),
	"exportTranscript": task(tL4),
	"revealTranscript": task(tL4),
	"paneInfo":         task(tL4),
	"closePane":        task(tL1),

	// Tabs.
	"newAgentTab":       task(tL1),
	"newAgentTabChoose": task(tL1),
	"newShellTab":       task(tL1),
	"nextTab":           task(tL1),
	"prevTab":           task(tL1),
	"selectTab":         task(tL1),
	"mergeAllTabs":      cut(whyRearrange),

	// Agents.
	"toggleBroadcast":       cut(whyBroadcast),
	"toggleBroadcastMember": cut(whyBroadcast),
	"promptAll":             cut(whyBroadcast + " It opens the prompt bar aimed at the focused pane, which the new prompt (paneSend) covers; with broadcast on it reached every member."),
	"makeBaton":             task(tL6),
	"agents":                task(tL8, "The list of agents across projects has no task; decide whether the graph's project switch is enough"),
	"closeFinishedPanes":    task(tL1),
	"startTeam":             task(tL1),
	"team":                  task(tL8, "The graph is the roster; the task board and messages dialog has no task"),
	"apiKeys":               task(tL5),

	// Git.
	"worktrees": task(tC7),
	"changes":   task(tC6),

	// Finding your way.
	"findInTerminal": task(tL4),
	"findPane":       task(tL4),
	"history":        task(tL4),

	// The window.
	"settings":       task(tL2),
	"fontUp":         task(tL2),
	"fontDown":       task(tL2),
	"fontReset":      task(tL2),
	"remote":         task(tL5),
	"helpers":        task(tL6),
	"openRecordings": task(tL4),
	"detach":         task(tL7),
	"quit":           task(tL7),
}

// cmdVerdicts classify every control-socket command the old interface sends
// that the new one does not. The new interface's own commands are not here.
var cmdVerdicts = map[string]Verdict{
	// Replaced by what the new interface sends.
	"sendPrompt":  replaced("paneSend", "Prompting one pane; prompting all is cut"),
	"teamWatch":   replaced("graphWatch", ""),
	"teamUnwatch": replaced("graphUnwatch", ""),

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
	"browse":             task(tL1, "The folder picker of the start flow"),
	"recents":            task(tL1, "Recent folders of the start flow"),
	"agents":             task(tL1, "The agent picker"),
	"refreshAgents":      task(tL1, "The agent picker"),
	"teamClose":          task(tL1),

	// L2: settings.
	"accentColor":      task(tL2),
	"theme":            task(tL2),
	"fontFamily":       task(tL2),
	"fontSize":         task(tL2),
	"cursorStyle":      task(tL2),
	"cursorBlink":      task(tL2),
	"scrollback":       task(tL2),
	"screenReader":     task(tL2),
	"statusLine":       task(tL2),
	"railExpanded":     task(tL2),
	"railWidth":        task(tL2),
	"setKeybinding":    task(tL2),
	"resetKeybinding":  task(tL2),
	"resetKeybindings": task(tL2),
	"resetTips":        task(tL2),
	"dismissTip":       task(tL2),
	"setAgentDefault":  task(tL2),
	"setAgentAddress":  task(tL2),

	// L3: help.
	"helpSeen": task(tL3, "The first-run welcome"),

	// L4: history, find, pane info, transcripts.
	"conversations":      task(tL4),
	"resumeConversation": task(tL4),
	"findPane":           task(tL4),
	"paneInfo":           task(tL4),
	"exportTranscript":   task(tL4),
	"revealTranscript":   task(tL4),
	"revealPane":         task(tL4),
	"recordPane":         task(tL4),
	"openRecordings":     task(tL4),

	// L5: API keys, remote, notifications.
	"keys":            task(tL5, "The API keys list"),
	"keySet":          task(tL5),
	"keyClear":        task(tL5),
	"remoteDevices":   task(tL5),
	"remoteEnable":    task(tL5),
	"remoteDisable":   task(tL5),
	"remotePair":      task(tL5),
	"remoteReconnect": task(tL5),
	"remoteRemove":    task(tL5),
	"remoteRename":    task(tL5),
	"remoteRevoke":    task(tL5),
	"remoteUnverify":  task(tL5),
	"remoteVerify":    task(tL5),
	"remoteMove":      task(tL5),
	"setDeviceRole":   task(tL5),
	"pushNotify":      task(tL5),
	"pushAnonymous":   task(tL5),
	"pushDelay":       task(tL5),
	"presence":        task(tL5, "Tells the desktop this window is in use, which notifications depend on"),
	"notifications":   task(tL5),

	// L6: helpers, baton, projects.
	"helpers":             task(tL6),
	"helperInstall":       task(tL6),
	"helperOpen":          task(tL6),
	"helperPlan":          task(tL6),
	"helperStart":         task(tL6),
	"helperStop":          task(tL6),
	"helperUninstall":     task(tL6),
	"makeBaton":           task(tL6),
	"saveBaton":           task(tL6, "Sent through a helper in the old interface"),
	"restartWithBaton":    task(tL6, "Sent through a helper in the old interface"),
	"startFromBaton":      task(tL6, "Sent through a helper in the old interface"),
	"approveBaton":        task(tL6, "Arrives from the desktop in a notice's action, not named in the old interface"),
	"openProject":         task(tL6),
	"closeProject":        task(tL6),
	"removeProject":       task(tL6),
	"archiveProject":      task(tL6),
	"groupProjects":       task(tL6),
	"addRepoToGroup":      task(tL6),
	"removeRepoFromGroup": task(tL6),
	"renameProject":       task(tL6),
	"renameGroup":         task(tL6),
	"reorderProjects":     task(tL6),
	"selectProject":       task(tL6, "Tells the desktop which project is active"),
	"selectRepo":          task(tL6),

	// L7: updates, detach, quit.
	"checkForUpdate": task(tL7),
	"installVersion": task(tL7),
	"listVersions":   task(tL7),
	"detach":         task(tL7),
	"quit":           task(tL7),
	"restart":        task(tL7, "Restart after an update"),
	"updates":        task(tL7),
	"save":           task(tL7, "Saves the layout when the page unloads; may be moot once tiling is cut"),

	// Git, worktrees, GitHub.
	"changes":        task(tC6),
	"commit":         task(tC6),
	"diff":           task(tC6),
	"gitFetch":       task(tC6),
	"gitPull":        task(tC6),
	"gitPush":        task(tC6),
	"worktrees":      task(tC7),
	"worktreeAdd":    task(tC7),
	"worktreePrune":  task(tC7),
	"worktreeRemove": task(tC7),
	"conflictRadar":  task(tC7),
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

	// The task board and messages. C5 shows a task; changing one has no task.
	"taskGet":    task(tC5),
	"taskList":   task(tC5),
	"taskAdd":    task(tL8, "Editing the task board has no task"),
	"taskAssign": task(tL8, "Editing the task board has no task"),
	"taskBranch": task(tL8, "Editing the task board has no task"),
	"taskNote":   task(tL8, "Editing the task board has no task"),
	"taskPrune":  task(tL8, "Editing the task board has no task"),
	"taskStatus": task(tL8, "Editing the task board has no task"),
	"msgList":    task(tL8, "Team messages have no task"),
	"msgSend":    task(tL8, "Team messages have no task"),
	"msgSeen":    task(tL8, "Team messages have no task"),

	// Routing and review, TypeSafe's Jev: docs are deferred until they ship.
	"autoReview":        task(tL8, "Safety control: the new shell cannot turn auto review off"),
	"autoReviewDefault": task(tL8, "Safety control: the default for new panes"),
	"setRouting":        task(tL8, "Routing has no task"),
	"setWeights":        task(tL8, "Routing has no task"),
	"clearRoutingLog":   task(tL8, "Routing has no task"),
	"jevKey":            task(tL8, "Security control: the Jev key is set and cleared here"),
	"jevStatus":         task(tL8, "The Jev status has no task"),
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
var blocksRetirement = []string{"autoReview", "autoReviewDefault", "jevKey"}
