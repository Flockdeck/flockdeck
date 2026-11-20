package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/artifacts"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// /ws/sealed is the sealed command socket (docs/remote-parity: stage 1). It
// carries the command and message vocabulary of /ws/control, over the
// end-to-end channel of /ws/pty and /ws/approvals, so a paired phone can be sent
// what the control socket's relay scrub holds back, because here the relay can
// neither read a frame nor forge one.
//
// What a device must have before it is told anything, checked when the socket
// opens, before every command and before every message written:
//
//   - the usual end-to-end key, on both sides, and a handshake that used it;
//   - the desk's mark that this key is verified (remoteVerify);
//   - the right to see panes: a full device, or a viewer the desk let watch them.
//
// It fails closed. A device without a verified key is closed out with the reason
// and is sent nothing; no frame is ever written in the clear; a command not in
// sealedCommands is refused without being run; a message not in sealedMessages is
// dropped.
//
// Tier A is a read, and any verified device with the right to see panes has it.
// Tier B (API 1.38, stage 3) acts on agents: send, stop, answer, a shell command,
// a new agent; from 1.39 (stage 5) also resuming a conversation, adding a
// worktree and moving a structured pane to another conversation. It needs the desk's "Control agents" switch for that device, which
// is tied to the key (sealed_control.go), looked at again before every request
// and every message written, and it is rate limited and logged. Every command a connection runs goes
// through the same handleCommand as a window's, as a window reached through the
// relay (controlClient.remote is set), so each refusal that holds for the
// unsealed socket holds here too, apart from the three that sealed lifts for the
// commands in sealedCommands (liftsRelayRefusal).

// sealedWireVersion is the version of the frames below, sent in the hello.
const sealedWireVersion = 1

// The limits a sealed connection keeps.
const (
	// sealedMaxRequest is the most a request, once opened, may be.
	sealedMaxRequest = 16 << 10
	// sealedMaxImageRequest is the most an attachImage request may be: a picture
	// the phone has already scaled down. It stays under the relay's 4 MiB per
	// tunnel session buffer.
	sealedMaxImageRequest = 3 << 20
	// sealedActBurst and sealedActRate limit tier B requests a device sends.
	sealedActBurst = 10
	sealedActRate  = 2.0
	// sealedSpawnBurst and sealedSpawnRate limit the tier B requests that start a
	// process or make a folder (sealedSpawning), on top of the limit above: 5 at
	// once, then one every 2 seconds.
	sealedSpawnBurst = 5
	sealedSpawnRate  = 0.5
	// sealedAskBurst and sealedAskRate limit the questions a device may put to the
	// desk (tier C): 3 at once, then one every 20 seconds, so a device cannot keep a
	// dialog in front of the person.
	sealedAskBurst = 3
	sealedAskRate  = 0.05
	// sealedPadBucket is the size every frame, in either direction, is rounded
	// up to a multiple of, so that the relay, which sees the size of each sealed
	// frame, does not learn how long a search or a reply is.
	sealedPadBucket = approvalPadBucket
	// sealedSocketsPerDevice is how many sockets one device may hold.
	sealedSocketsPerDevice = 2
	// sealedConnectsPerMinute is how many sockets a device may open in a minute,
	// whether or not they get past the checks.
	sealedConnectsPerMinute = 20
	// sealedFrameBurst and sealedFrameRate limit the requests a device sends.
	sealedFrameBurst = 40
	sealedFrameRate  = 20.0
	// sealedBusyMax is how many requests in a row may be turned away for rate
	// before the socket is closed, and sealedAbuseMax how many frames of the kind
	// a correct client never sends are put up with in a row.
	sealedBusyMax  = 20
	sealedAbuseMax = 3
	// sealedWriteTimeout bounds one write to the device.
	sealedWriteTimeout = 10 * time.Second
	// sealedFirstFrame is how long a socket may stay open without a request.
	sealedFirstFrame = time.Minute
)

// What the socket says in a close, besides the approval reasons it shares.
const (
	// sealedReasonAccess: the device may not see panes.
	sealedReasonAccess = "access"
	// sealedReasonOff: the desk ended the socket (a role change, a verification
	// taken back, the device unpaired).
	sealedReasonOff = "disabled"
)

// sealedTier is what a command or message may be used for. A reads. B acts on
// agents and needs "Control agents" (sealed_control.go).
const (
	sealedTierA = "A"
	sealedTierB = "B"
	// sealedTierC acts on the repository (commit, push, pull) and needs what tier B
	// needs and a person at the desk saying yes to each request (sealed_confirm.go).
	sealedTierC = "C"
)

// sealedCommands is every command the sealed socket runs, with its tier: the
// default is that a command is refused. api/catalog.json lists the table (the
// "sealed" field of a command) and TestSealedTablesAreInTheCatalog holds the two
// together, so a command is made reachable here by an edit that is read.
//
// Tier A is a read: the structured conversation of a pane and its pushes, a
// terminal pane's conversation, and the changes and the diff of a folder. None
// writes, sends text to an agent or starts anything. Reads of repository files
// (the changes and diff family) are for a full device only (sealedFullOnly),
// as they are for the unsealed table (commandAccess).
var sealedCommands = map[string]string{
	"structuredSnapshot": sealedTierA, "structuredWatch": sealedTierA, "structuredUnwatch": sealedTierA,
	"conversationOpen": sealedTierA, "conversationOlder": sealedTierA, "conversationDetail": sealedTierA,
	"conversationSearch": sealedTierA, "conversationClose": sealedTierA,
	"changes": sealedTierA, "diff": sealedTierA,

	// Tier A, changes and checks (API 1.40, stage 6): the branch list of a project,
	// a branch's files in pages, a file's hunks and the lines around one, and the
	// CI status of a folder. Reads of repository content, redacted like the rest.
	// Not here: the watch (it pushes for as long as the socket is open), the
	// comments, reviews and merge gate that write (commentAdd, requestReview,
	// approveMerge, mergeBranch) and running a project's check commands.
	"changesSnapshot": sealedTierA, "filesPage": sealedTierA, "diffHunks": sealedTierA, "diffContext": sealedTierA,
	"ghChecks": sealedTierA,

	// Tier A, start flows and history (API 1.39, stage 5): what a person looks at
	// before starting or resuming something. The folders Flockdeck knows and the
	// recent projects, a folder's listing inside them, the past conversations of
	// a folder, the worktrees and branches of a repo, and the past conversations
	// a structured pane could move to. Each is for a full device (sealedFullOnly).
	"conversations": sealedTierA, "worktrees": sealedTierA, "recents": sealedTierA, "browse": sealedTierA,
	"structuredSessions": sealedTierA,

	// Tier B. Text for an agent (a message, a queued one edited or sent now, a
	// picture to go with it), stopping a turn, the answer to a question or a plan,
	// a person's one-line shell command in a structured pane, and a new agent with
	// its first prompt. Not here, on purpose: structuredSet (a mode such as
	// bypassing permissions), allow-always answers, tool approvals (stage 4),
	// commit and push (stage 6), merges.
	"paneSend": sealedTierB, "attachImage": sealedTierB, "structuredCancel": sealedTierB,
	"structuredQueueEdit": sealedTierB, "structuredQueueRemove": sealedTierB, "structuredQueueClear": sealedTierB, "structuredQueueSendNow": sealedTierB,
	"structuredShellRun": sealedTierB, "structuredShellStop": sealedTierB, "structuredShellRemove": sealedTierB, "structuredShellSend": sealedTierB,
	"permissionAnswer": sealedTierB, "startAgent": sealedTierB,

	// Tier B, stage 5: pick a conversation up again in a folder of a known
	// project, move a structured pane to another conversation or a clean one, and
	// add a worktree. Not here: removing or pruning a worktree (it destroys work,
	// so it stays for the desk until a confirm at the desk exists), and anything
	// that names a folder outside the known projects.
	"resumeConversation": sealedTierB, "worktreeAdd": sealedTierB,
	"structuredResume": sealedTierB, "structuredNewConversation": sealedTierB,

	// Tier C, stage 6: commit, push and pull. Each request is held until a person at
	// the desk answers it (sealed_confirm.go), asked again for the next. Merges are
	// not here; they are the desk's. deskConfirmAnswer is not here either.
	"commit": sealedTierC, "gitPush": sealedTierC, "gitPull": sealedTierC,

	// Tier A, the team view (API 1.41, stage 7): the roster, the graph with what
	// each agent needs and its live line, the goal of each team, and the mail
	// between agents. A watch is allowed: it is sent to this socket only and ends
	// with it, at most every teamInterval. Each is for a full device
	// (sealedFullOnly), because the roster names every pane of the project.
	"teamSnapshot": sealedTierA, "teamWatch": sealedTierA, "teamUnwatch": sealedTierA,
	"graphSnapshot": sealedTierA, "graphWatch": sealedTierA, "graphUnwatch": sealedTierA,
	"msgList": sealedTierA,

	// Tier B, stage 7: a message from the person to a pane, a name or a role of
	// the project (as the Team panel sends it), and the person's own "seen" mark
	// on a message. Not here, on purpose: the task board (taskList, taskAdd and the
	// rest: T203 removes it), teamClose (it closes panes), setRouting and
	// clearRoutingLog (who gets a task is the desk's), merges.
	"msgSend": sealedTierB, "msgSeen": sealedTierB,

	// Stage 8 (API 1.42). Tier A: the push preferences as they stand; the list of
	// paired devices, cut down (full device only); unpairing the device itself and
	// no other (sealedSelfOnly); GitHub reads for a full device. Tier C: a comment on a pull request or an issue, asked of
	// the desk each time. Not here, on purpose: remotePair, remoteRename,
	// setDeviceRole, setDeviceControl, remoteVerify, remoteEnable and the other
	// commands that change who can reach or control this machine, ghPRCreate,
	// ghIssueCreate, ghLogin, ghLogout, ghInstall, merges, and the three push
	// settings (pushNotify, pushAnonymous, pushDelay): they change what every
	// paired device is sent, no prompt guards them, so they are the desk's.
	"pushPrefsGet": sealedTierA, "remoteDevices": sealedTierA, "remoteRevoke": sealedTierA,
	"ghStatus": sealedTierA, "ghPRs": sealedTierA, "ghPR": sealedTierA, "ghIssues": sealedTierA, "ghIssue": sealedTierA,
	"ghPRComment": sealedTierC, "ghIssueComment": sealedTierC,
}

// sealedFullOnly are the tier A commands only a full device is let run. A viewer
// the desk let watch panes reads the conversation of the panes it sees. It is
// not let read repository files (the changes and the diff of a folder, a
// branch's files, hunks, CI status), list every conversation of a folder, every
// branch or the folders of the machine's projects. allowCommand lets every
// sealedCommands entry through for a sealed connection, so this table is the
// only role check these commands get.
var sealedFullOnly = map[string]bool{
	"conversations": true, "worktrees": true, "recents": true, "browse": true, "structuredSessions": true,
	"teamSnapshot": true, "teamWatch": true, "teamUnwatch": true,
	"graphSnapshot": true, "graphWatch": true, "graphUnwatch": true, "msgList": true,
	"remoteDevices": true, "ghStatus": true, "ghPRs": true, "ghPR": true, "ghIssues": true, "ghIssue": true,
	"changes": true, "diff": true, "changesSnapshot": true, "filesPage": true,
	"diffHunks": true, "diffContext": true, "ghChecks": true,
}

// sealedSpawning are the tier B commands that make something run or write a
// folder: a new process, a resumed conversation, a worktree. A device is let
// do five in ten seconds, whatever else it sends (sealedSpawnBurst).
var sealedSpawning = map[string]bool{
	"startAgent": true, "resumeConversation": true, "worktreeAdd": true,
}

// sealedMessage says how a message type reaches a sealed device.
type sealedMessage struct {
	Tier string
	// scrub, when set, rewrites the message and may drop it, as the relay's scrubs
	// do. It is for the answers that only refuse something: the text of a refusal
	// is redacted and cut to a line, and an answer that holds data is dropped. A
	// message with no scrub is sent redacted, whole (redactSealed).
	scrub func([]byte) []byte
	// scrubAct, when set, is used in place of scrub for a device that may act.
	scrubAct func([]byte) []byte
	// strip is the top-level fields taken out first: capabilities a read-only
	// connection has no use for.
	strip []string
}

// sealedMessages is every message type a sealed device is sent, with how.
// Everything else, the state snapshot, the agents list, the graph, hello, what
// the desk asked of its own windows, is dropped.
var sealedMessages = map[string]sealedMessage{
	// The pane's structured conversation, pushed and answered: its entries, tool
	// output, plan and permission card. The rewind token lets a window go back to
	// an earlier prompt, which is an action, so it is not sent.
	"structured":         {Tier: sealedTierA, strip: []string{"rewindToken"}},
	"conversationPage":   {Tier: sealedTierA},
	"conversationAppend": {Tier: sealedTierA},
	"conversationDetail": {Tier: sealedTierA},
	"conversationSearch": {Tier: sealedTierA},
	"changes":            {Tier: sealedTierA},
	"diff":               {Tier: sealedTierA},
	// Start flows and history. The history list holds the first prompt of each
	// conversation, so it is redacted like any other text; the folder names are
	// the known projects' own.
	"conversations": {Tier: sealedTierA},
	"worktrees":     {Tier: sealedTierA},
	// The changes view (1.40): the branch list, a branch's files, hunks, the lines
	// around one, and the CI status.
	"changesTeam": {Tier: sealedTierA}, "changesFiles": {Tier: sealedTierA},
	"diffHunks": {Tier: sealedTierA}, "diffContext": {Tier: sealedTierA}, "ghChecks": {Tier: sealedTierA},
	"recents": {Tier: sealedTierA},
	"browse":  {Tier: sealedTierA},
	// The team view (1.41). The team message is the Team panel's whole picture; its
	// task board is taken out (T203 removes the board and no task command runs
	// here), which leaves the roster, the counts and the mail. teamGone says a
	// project can no longer be read, in one redacted line.
	"team":     {Tier: sealedTierA, strip: []string{"tasks", "openHidden", "finishedHidden", "boardError"}},
	"messages": {Tier: sealedTierA},
	"graph":    {Tier: sealedTierA},
	"teamGone": {Tier: sealedTierA, scrub: scrubTeamGone},
	// Stage 8 (1.42): the push preferences, the cut-down device list and how
	// unpairing the device itself ended, and GitHub. A pull request or an issue is
	// cut to a size and redacted (scrubGHView); a list holds titles only.
	"pushPrefs": {Tier: sealedTierA}, "deviceRoster": {Tier: sealedTierA}, "deviceRevoked": {Tier: sealedTierA},
	"ghStatus": {Tier: sealedTierA}, "ghPRs": {Tier: sealedTierA}, "ghIssues": {Tier: sealedTierA},
	"ghPR": {Tier: sealedTierA, scrub: scrubGHView}, "ghIssue": {Tier: sealedTierA, scrub: scrubGHView},
	// The refusals of a command, in the words the relay is given.
	"teamResult":    {Tier: sealedTierA, scrub: scrubTeamResultSealed, scrubAct: scrubTeamResultAct},
	"changesResult": {Tier: sealedTierA, scrub: scrubChangesResultSealed},
	"notice":        {Tier: sealedTierA, scrub: scrubNotice},
	// The answers to tier B commands. A device without the right is sent none.
	"paneSend":          {Tier: sealedTierB},
	"permissionAnswer":  {Tier: sealedTierB},
	"attachImageResult": {Tier: sealedTierB},
	"agentStarted":      {Tier: sealedTierB},
	// Where a commit, push or pull waits: the state only, no text.
	"gitConfirm": {Tier: sealedTierB},
}

// liftsRelayRefusal reports whether a command from this connection is let past
// the refusals a window reached through the relay is given: only a sealed
// connection, and only a command that sealedCommands names.
func (c *controlClient) liftsRelayRefusal(cmd string) bool {
	return c.sealed != nil && sealedCommands[cmd] != ""
}

// sealedWrite is the one place a message is made fit for a sealed device, as
// scrubForRemote is for the relay. It skips that scrub, which holds back the
// conversation, and applies the tier table and redaction instead. It returns
// nil to send nothing, and sends nothing for a message it cannot read: the
// connection is never sent what could not be checked.
//
// act says whether the device may act (tier B). It is called only for a message
// that needs it, so that the check is made once per such message and not per
// frame.
func sealedWrite(data []byte, act func() bool) []byte {
	rule, ok := sealedMessages[messageType(data)]
	if !ok || (rule.Tier != sealedTierA && rule.Tier != sealedTierB) || !hasOneTypeKey(data) {
		return nil
	}
	if rule.Tier == sealedTierB && (act == nil || !act()) {
		return nil
	}
	// A scrub cuts the one field it knows to a line; every other string of the
	// message is then redacted as for any other, so a field it did not expect is
	// not sent in the clear.
	if rule.scrubAct != nil && act != nil && act() {
		return redactScrubbed(rule.scrubAct(data), rule.strip)
	}
	if rule.scrub != nil {
		return redactScrubbed(rule.scrub(data), rule.strip)
	}
	return redactSealed(data, rule.strip)
}

// redactSealed is a message with every string in it redacted (the baton
// redactor, record.Redact) and the fields in strip taken out. A phone is
// easy to lose, so what is sent to it is held to the rule the recordings are,
// even though the channel is sealed. A picture's bytes are not text and are
// left as they are: base64 of an image is never a readable secret, and the
// redactor reads a long run of it as one to be cut.
func redactSealed(data []byte, strip []string) []byte {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v map[string]any
	if dec.Decode(&v) != nil {
		return nil
	}
	for _, k := range strip {
		delete(v, k)
	}
	out, err := json.Marshal(redactTree(v, ""))
	if err != nil {
		return nil
	}
	return out
}

// redactTree redacts every string in a decoded message (record.Redact), except a
// string under the key "data" that is a run of base64. A value is replaced by
// the name of its key only by record.RedactValue, which is not used: it reads
// "inputTokens" as a secret and would replace a count with a word.
func redactTree(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = redactTree(e, k)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = redactTree(e, key)
		}
		return out
	case string:
		if key == "data" && isBase64Run(x) {
			return x
		}
		return record.Redact(x)
	}
	return v
}

// isBase64Run reports whether s is at least 64 characters of the base64 alphabet
// and nothing else.
func isBase64Run(s string) bool {
	if len(s) < 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '+', c == '/', c == '=':
		default:
			return false
		}
	}
	return true
}

// padSealed is a JSON object with a "pad" field of zeros added so that its
// length is a multiple of sealedPadBucket, as padded does for the approvals
// socket, for a message that is already bytes.
func padSealed(b []byte) []byte {
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return nil
	}
	const wrap = len(`,"pad":""`)
	n := len(b) + wrap
	target := (n + sealedPadBucket - 1) / sealedPadBucket * sealedPadBucket
	out := make([]byte, 0, target)
	out = append(out, b[:len(b)-1]...)
	out = append(out, `,"pad":"`...)
	out = append(out, bytes.Repeat([]byte{'0'}, target-n)...)
	out = append(out, `"}`...)
	return out
}

// sealedState is what the server keeps about the sealed socket.
type sealedState struct {
	mu      sync.Mutex
	open    map[string]int
	devices map[string]*sealedDevice
	// timing is what a test changes for one server.
	timing sealedTimings
}

// sealedTimings are read once when a socket is accepted.
type sealedTimings struct {
	first, handshake, roster time.Duration
	// recheck is how often a live socket is looked at against the roster and the
	// desk's settings again, and maxAge is the longest a socket lives.
	recheck, maxAge time.Duration
}

func (t sealedTimings) withDefaults() sealedTimings {
	if t.first == 0 {
		t.first = sealedFirstFrame
	}
	if t.handshake == 0 {
		t.handshake = 20 * time.Second
	}
	if t.roster == 0 {
		t.roster = 10 * time.Second
	}
	if t.recheck == 0 {
		t.recheck = 30 * time.Second
	}
	if t.maxAge == 0 {
		t.maxAge = 12 * time.Hour
	}
	return t
}

// sealedDevice is what every socket of one device shares, so that opening
// several does not multiply its allowance.
type sealedDevice struct {
	frames, connects, acts, spawns, asks *artifacts.Limiter

	// dmu guards the desk's refusals of this device's questions: how many the
	// person at the desk has said no to (or let lapse), and when the next one
	// may be asked. See noteDenied.
	dmu       sync.Mutex
	upSince   time.Time
	upBytes   int64
	denials   int
	lastDeny  time.Time
	askNotBef time.Time
}

// sealedDeviceMax is how many devices keep an allowance in memory at once.
const sealedDeviceMax = 1024

const (
	// sealedDenyBase is the wait after the first refused question; each further
	// one within sealedDenyMemory doubles it, up to sealedDenyMax.
	sealedDenyBase   = 20 * time.Second
	sealedDenyMax    = 15 * time.Minute
	sealedDenyMemory = time.Hour
)

// noteDenied records that the desk said no to a question of this device and
// lengthens the wait before it may ask again: a stolen phone cannot raise a modal
// at the desk every few seconds for as long as it likes. Only an explicit no
// counts toward the doubling; a question left to lapse is noteLapsed.
func (d *sealedDevice) noteDenied(now time.Time) {
	d.dmu.Lock()
	defer d.dmu.Unlock()
	if now.Sub(d.lastDeny) > sealedDenyMemory {
		d.denials = 0
	}
	d.denials++
	d.lastDeny = now
	wait := sealedDenyBase
	for i := 1; i < d.denials && wait < sealedDenyMax; i++ {
		wait *= 2
	}
	if wait > sealedDenyMax {
		wait = sealedDenyMax
	}
	d.askNotBef = now.Add(wait)
}

// noteLapsed records a question nobody answered. Nobody at the desk may simply
// have been away, so it is not counted as a refusal: it costs the base wait (so a
// phone cannot raise a modal every few seconds) and does not move the doubling,
// which would otherwise push the wait to its longest without anyone having said no.
func (d *sealedDevice) noteLapsed(now time.Time) {
	d.dmu.Lock()
	defer d.dmu.Unlock()
	if at := now.Add(sealedDenyBase); at.After(d.askNotBef) {
		d.askNotBef = at
	}
}

// noteAllowed clears the record: a person who says yes is using the phone.
func (d *sealedDevice) noteAllowed() {
	d.dmu.Lock()
	d.denials, d.askNotBef = 0, time.Time{}
	d.dmu.Unlock()
}

// mayAskAt reports whether the wait after the last refusal is over.
func (d *sealedDevice) mayAskAt(now time.Time) bool { return d.waitAt(now) == 0 }

// waitAt is how long is left of the wait after the last refusal, or 0.
func (d *sealedDevice) waitAt(now time.Time) time.Duration {
	d.dmu.Lock()
	defer d.dmu.Unlock()
	if now.Before(d.askNotBef) {
		return d.askNotBef.Sub(now)
	}
	return 0
}

func (st *sealedState) device(id string) *sealedDevice {
	st.mu.Lock()
	defer st.mu.Unlock()
	if d := st.devices[id]; d != nil {
		return d
	}
	if st.devices == nil {
		st.devices = map[string]*sealedDevice{}
	}
	// Full: drop one entry, not the table, so a flood of new ids cannot reset
	// every device's allowance. One with no socket open is chosen when there is
	// one; the order of a map is the only randomness needed.
	if len(st.devices) >= sealedDeviceMax {
		victim := ""
		for id := range st.devices {
			if victim == "" {
				victim = id
			}
			if st.open[id] == 0 {
				victim = id
				break
			}
		}
		delete(st.devices, victim)
	}
	fr, _ := artifacts.NewLimiter(sealedFrameRate, sealedFrameBurst, nil)
	cn, _ := artifacts.NewLimiter(float64(sealedConnectsPerMinute)/60, sealedConnectsPerMinute, nil)
	ac, _ := artifacts.NewLimiter(sealedActRate, sealedActBurst, nil)
	sp, _ := artifacts.NewLimiter(sealedSpawnRate, sealedSpawnBurst, nil)
	ak, _ := artifacts.NewLimiter(sealedAskRate, sealedAskBurst, nil)
	d := &sealedDevice{frames: fr, connects: cn, acts: ac, spawns: sp, asks: ak}
	st.devices[id] = d
	return d
}

func (st *sealedState) take(device string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.open[device] >= sealedSocketsPerDevice {
		return false
	}
	if st.open == nil {
		st.open = map[string]int{}
	}
	st.open[device]++
	return true
}

func (st *sealedState) release(device string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.open[device]--; st.open[device] <= 0 {
		delete(st.open, device)
	}
}

func (st *sealedState) timings() sealedTimings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.timing.withDefaults()
}

// sealedCheck decides whether device may be sent anything now, from the running
// program's record and the preferences. key is the device's usual end-to-end key
// as the handshake proved it, never one read from a roster or sent by the
// device. It fails closed, and returns the reason a device is refused, or "".
func (s *Server) sealedCheck(device string, origin remote.KeyOrigin, key []byte) string {
	if origin != remote.KeyOriginUsual {
		return approvalReasonOrigin
	}
	if a := s.deviceAccess(device); a.EffectiveRole() != store.RoleFull && !a.WatchPanes {
		return sealedReasonAccess
	}
	v := s.deviceVerifier()
	if v == nil || len(key) == 0 || v.DeviceVerifyState(device, origin, key) != remote.Verified {
		return approvalReasonVerify
	}
	return ""
}

// sealedLink is one open sealed socket: the encrypted channel, and what is
// needed to look again at whether the device may be served.
type sealedLink struct {
	s            *Server
	device, name string
	origin       remote.KeyOrigin
	key          []byte
	tc           *e2eConn
	refuse       func(code int, reason string)
	// cutCh is closed when the desk ends the socket.
	cutCh  chan struct{}
	cutOne sync.Once

	// wmu serialises writes: the e2e session's seal is not safe for two at once,
	// and the hello, the refusals and the control client's writer all write.
	wmu sync.Mutex
}

func (l *sealedLink) cut() { l.cutOne.Do(func() { close(l.cutCh) }) }

// reasonOff is why the device may not be served now, or "".
func (l *sealedLink) reasonOff() string {
	select {
	case <-l.cutCh:
		return sealedReasonOff
	default:
	}
	return l.s.sealedCheck(l.device, l.origin, l.key)
}

// gate is reasonOff, closing the socket when it is not empty.
func (l *sealedLink) gate() bool {
	reason := l.reasonOff()
	if reason == "" {
		return true
	}
	select {
	case <-l.cutCh:
	default:
		l.s.sealedDenied(l.device, l.name, reason, false)
	}
	l.refuse(artifactCloseRefused, reason)
	return false
}

// mayAct is whether the device may act on agents now: the desk's switch, for the
// key this connection proved, in the record in force and in the file.
func (l *sealedLink) mayAct() bool {
	return l.reasonOff() == "" && l.s.mayControlWithKey(l.device, l.key)
}

// write is controlClient.write for a sealed connection: the gate, the tier
// table and redaction, padding, then the seal. It reports whether the
// connection is still usable; a message that is not sent is not a failure.
func (l *sealedLink) write(ctx context.Context, data []byte) bool {
	if !l.gate() {
		return false
	}
	out := sealedWrite(data, l.mayAct)
	if out == nil {
		return true
	}
	return l.writeFrame(ctx, out)
}

// writeFrame pads and seals one frame the host made itself or that sealedWrite
// passed. It is the only write to the connection.
func (l *sealedLink) writeFrame(ctx context.Context, frame []byte) bool {
	padded := padSealed(frame)
	if padded == nil {
		return true
	}
	wctx, cancel := context.WithTimeout(ctx, sealedWriteTimeout)
	defer cancel()
	l.wmu.Lock()
	defer l.wmu.Unlock()
	return l.tc.Write(wctx, websocket.MessageText, padded) == nil
}

// sealedDenied records a refusal of a socket or a device in the denied log, at a
// limited rate. The desk is told only of what a correct client never causes
// (notify): a phone that has not been verified yet asks, and is refused with
// the reason, without bothering anybody.
func (s *Server) sealedDenied(device, name, reason string, notify bool) {
	record, note, suppressed := s.approvals.denial(device+"\x00sealed:"+reason, true)
	if record {
		s.logLost(s.approvals.deniedLog.write(auditEvent{Event: "sealed-denied", Device: device, DeviceName: name, Reason: reason, Suppressed: suppressed}))
	}
	if notify && note {
		who := tidy(name)
		if who == "" {
			who = "A paired device"
		}
		s.noticeDesk(who+" was refused the sealed connection ("+tidy(reason)+"). That is not something Flockdeck's own phone app does", true)
	}
}

// sealedHello is the first frame the host sends once the device is let in.
type sealedHello struct {
	Type       string   `json:"type"`
	V          int      `json:"v"`
	Desk       string   `json:"desk"`
	Device     string   `json:"device"`
	API        *apiInfo `json:"api"`
	Tier       string   `json:"tier"`
	PadBucket  int      `json:"padBucket"`
	MaxRequest int      `json:"maxRequest"`
	// MaxImageRequest is the most an attachImage request may be (tier B).
	MaxImageRequest int      `json:"maxImageRequest,omitempty"`
	Commands        []string `json:"commands"`
}

// sealedRefusal answers a request that was not run.
type sealedRefusal struct {
	Type string `json:"type"`
	V    int    `json:"v"`
	Req  string `json:"req,omitempty"`
	Cmd  string `json:"cmd,omitempty"`
	Code string `json:"code"`
}

// The codes of a sealedRefusal. None carries text from an agent.
const (
	sealedCodeCommand = "command" // not a command this socket runs
	sealedCodeBusy    = "busy"    // too many requests too fast
	sealedCodeShape   = "shape"   // not a command, or not padded
	// sealedCodeControl: a tier B command from a device the desk has not let
	// control agents.
	sealedCodeControl = "control"
	// sealedCodeRole: a command only a full device may run, from a viewer.
	sealedCodeRole = "role"
)

// sealedCommandNames is the commands the socket runs for a device, sorted, as
// the hello says: tier A only unless the device may act.
func sealedCommandNames(act bool) []string {
	out := make([]string, 0, len(sealedCommands))
	for name, tier := range sealedCommands {
		if tier != sealedTierA && !act {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// handleSealed serves /ws/sealed.
func (s *Server) handleSealed(w http.ResponseWriter, r *http.Request) {
	// Only through the relay. Loopback cannot forge the device mark, and the desk
	// has no use for this socket.
	if !fromRemote(r) {
		http.NotFound(w, r)
		return
	}
	device := r.Header.Get("Flockdeck-Remote-Device")
	// A browser always sends Origin on a WebSocket; one without is not a page.
	if device == "" || r.Header.Get("Origin") == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := tidy(r.Header.Get("Flockdeck-Remote-Device-Name"))
	origin := remote.KeyOriginUsual
	if r.Header.Get("Flockdeck-Remote-Origin") == "desk" {
		origin = remote.KeyOriginDesk
	}

	// No origin patterns and no compression: this is ciphertext.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(sealedMaxImageRequest + 1024)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-s.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	refuse := func(code int, reason string) {
		_ = conn.Close(websocket.StatusCode(code), reason)
	}
	tm := s.sealed.timings()
	var link *sealedLink
	// Questions put to the desk for this socket end with it.
	defer func() {
		if link != nil {
			s.dropConfirms(link)
		}
	}()

	l := &sealedLink{s: s, device: device, name: name, origin: origin, refuse: refuse, cutCh: make(chan struct{})}
	link = l
	// Registered before the device is checked, so that a change of role between
	// the two ends it (s.sockets.cut).
	forget := s.sockets.add(device, func() {
		l.cut()
		go func() { _ = conn.Close(websocket.StatusCode(artifactCloseRefused), sealedReasonOff) }()
	})
	defer forget()

	// Encryption is not optional: both sides must have a key on file.
	ra := s.remoteAccess()
	pctx, pcancel := context.WithTimeout(ctx, tm.roster)
	defer pcancel()
	if ra == nil || !ra.E2ECapable(pctx, device, origin) {
		s.sealedDenied(device, name, approvalReasonKey, false)
		refuse(artifactCloseRefused, approvalReasonKey)
		return
	}
	// Before spending a handshake, what can be known without the key. The check
	// that decides is made below with the key the handshake used.
	if origin != remote.KeyOriginUsual {
		s.sealedDenied(device, name, approvalReasonOrigin, false)
		refuse(artifactCloseRefused, approvalReasonOrigin)
		return
	}
	if a := s.deviceAccess(device); a.EffectiveRole() != store.RoleFull && !a.WatchPanes {
		s.sealedDenied(device, name, sealedReasonAccess, false)
		refuse(artifactCloseRefused, sealedReasonAccess)
		return
	}
	// Past the roster and the access checks: only now does the device get an
	// allowance in memory, so an id that is not paired allocates nothing.
	dev := s.sealed.device(device)
	if !dev.connects.Allow(1) {
		s.sealedDenied(device, name, "connect-rate", true)
		refuse(artifactCloseBusy, artifactErrBusy)
		return
	}
	if !s.sealed.take(device) {
		s.sealedDenied(device, name, artifactErrBusy, true)
		refuse(artifactCloseBusy, artifactErrBusy)
		return
	}
	defer s.sealed.release(device)
	hctx, hcancel := context.WithTimeout(ctx, tm.handshake)
	sess, err := s.e2eHandshake(hctx, ra, conn, device, origin)
	hcancel()
	if err != nil {
		// A failed handshake is a fault or a downgrade attempt, never a reason to
		// go on unencrypted.
		s.sealedDenied(device, name, "handshake", true)
		refuse(int(websocket.StatusPolicyViolation), "end-to-end handshake failed")
		return
	}
	l.key = sess.DeviceStaticKey()
	l.tc = &e2eConn{conn: conn, sess: sess}
	if !l.gate() {
		return
	}

	// The connection is a window of the control socket's own kind, reached
	// through the relay, whose every write goes through the link and not to the
	// socket.
	c := &controlClient{
		out:    make(chan []byte, 64),
		ready:  make(chan struct{}, 1),
		remote: true,
		device: device,
		sealed: l,
	}
	c.accessOf = func() store.DeviceAccess { return s.deviceAccess(device) }
	s.mu.Lock()
	select {
	case <-s.closed:
		s.mu.Unlock()
		return
	default:
	}
	s.connWG.Add(1)
	s.clients[c] = true
	s.mu.Unlock()
	defer s.connWG.Done()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		// A window that has gone is watching no pane's conversation either.
		s.convos.dropClient(c)
		s.viewersChanged(c)
	}()

	s.connWG.Add(1)
	go func() {
		defer s.connWG.Done()
		c.writeLoop(ctx)
		// The writer ends when a write fails or the gate closes the socket; the
		// reader below is waiting on a read, so it is woken.
		cancel()
	}()
	s.connWG.Add(1)
	go func() {
		defer s.connWG.Done()
		keepalive(ctx, cancel, conn, new(writeGauge), s.pingInterval, s.pingTimeout)
	}()
	s.viewersChanged(c)

	// A live socket is looked at again, whether or not it sends anything: a
	// device taken off the roster or no longer verified is cut within
	// tm.recheck, and no socket outlives tm.maxAge.
	s.connWG.Add(1)
	go func() {
		defer s.connWG.Done()
		tick := time.NewTicker(tm.recheck)
		defer tick.Stop()
		born := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if time.Since(born) > tm.maxAge {
				s.sealedDenied(device, name, "session-age", false)
				refuse(artifactCloseRefused, sealedReasonOff)
				return
			}
			if !l.gate() {
				return
			}
			rctx, rcancel := context.WithTimeout(ctx, tm.roster)
			ok := ra.E2ECapable(rctx, device, origin)
			rcancel()
			if ctx.Err() != nil {
				return
			}
			if !ok {
				s.sealedDenied(device, name, approvalReasonKey, false)
				refuse(artifactCloseRefused, approvalReasonKey)
				return
			}
		}
	}()

	helloAct := s.mayControlWithKey(device, l.key)
	helloTier := sealedTierA
	if helloAct {
		helloTier = sealedTierB
	}
	if !l.writeFrame(ctx, mustMarshal(sealedHello{
		Type: "sealedHello", V: sealedWireVersion, Desk: s.deskName(), Device: name, API: currentAPI(), Tier: helloTier,
		PadBucket: sealedPadBucket, MaxRequest: sealedMaxRequest, MaxImageRequest: sealedMaxImageRequest, Commands: sealedCommandNames(helloAct),
	})) {
		return
	}

	var busy, abuse int
	wait := tm.first
	for {
		rc, rcl := context.WithTimeout(ctx, wait)
		wait = 24 * time.Hour
		typ, data, err := l.tc.Read(rc)
		rcl()
		if err != nil {
			return
		}
		// The gate is looked at again before a request is acted on, so a change
		// made at the desk takes effect on the next one.
		if !l.gate() {
			return
		}
		if !dev.frames.Allow(1) {
			if busy++; busy > sealedBusyMax {
				s.sealedDenied(device, name, "rate", true)
				refuse(artifactCloseBusy, artifactErrBusy)
				return
			}
			if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Code: sealedCodeBusy})) {
				return
			}
			continue
		}
		busy = 0
		var cmd command
		if typ != websocket.MessageText || len(data) == 0 || len(data) > sealedMaxImageRequest || len(data)%sealedPadBucket != 0 || json.Unmarshal(data, &cmd) != nil {
			// A request is padded to a multiple of the bucket, so that a search is
			// not told by its length. One that is not is the sign of a client that
			// is not the phone's.
			if abuse++; abuse >= sealedAbuseMax {
				s.sealedDenied(device, name, "malformed", true)
				refuse(artifactCloseRefused, "refused")
				return
			}
			if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Code: sealedCodeShape})) {
				return
			}
			continue
		}
		abuse = 0
		if cmd.Cmd == "" {
			// A frame with a type and no command (clientHello) has nothing to do here.
			continue
		}
		if sealedCommands[cmd.Cmd] == "" {
			// Not run, not looked at by handleCommand at all. A newer phone may ask
			// for more than this desk offers, so it is told and not cut off.
			if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeCommand})) {
				return
			}
			continue
		}
		if len(data) > sealedMaxRequest && cmd.Cmd != "attachImage" {
			// Only a picture is allowed to be long.
			if abuse++; abuse >= sealedAbuseMax {
				s.sealedDenied(device, name, "malformed", true)
				refuse(artifactCloseRefused, "refused")
				return
			}
			if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeShape})) {
				return
			}
			continue
		}
		if sealedFullOnly[cmd.Cmd] && s.deviceAccess(device).EffectiveRole() != store.RoleFull {
			s.sealedDenied(device, name, sealedCodeRole, false)
			if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeRole})) {
				return
			}
			continue
		}
		if sealedSelfOnly[cmd.Cmd] {
			// A command that is for the device itself only: another device's id is
			// refused, and the rate and the log are those of tier B, so that a
			// device cannot use it to fill either.
			if cmd.ID == "" || cmd.ID != device {
				s.sealedDenied(device, name, sealedCodeSelf, false)
				if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeSelf})) {
					return
				}
				continue
			}
			if !dev.acts.Allow(1) {
				if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeBusy})) {
					return
				}
				continue
			}
		}
		if tier := sealedCommands[cmd.Cmd]; tier == sealedTierB || tier == sealedTierC {
			// Looked at again for every request, so that taking the right away
			// at the desk, or a key that changed, takes effect on the next one.
			if !l.mayAct() {
				s.sealedDenied(device, name, sealedCodeControl, false)
				if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeControl})) {
					return
				}
				continue
			}
			if !dev.acts.Allow(1) {
				if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeBusy})) {
					return
				}
				continue
			}
			if sealedSpawning[cmd.Cmd] && !dev.spawns.Allow(1) {
				if !l.writeFrame(ctx, mustMarshal(sealedRefusal{Type: "sealedRefused", V: sealedWireVersion, Req: clampReq(cmd.Req), Cmd: clampSealedCmd(cmd.Cmd), Code: sealedCodeBusy})) {
					return
				}
				continue
			}
			// What was done, never what it said: no text, picture or command line.
			s.approvals.auditAsync(auditEvent{Event: "sealed-act", Device: device, DeviceName: name, Kind: cmd.Cmd, Pane: clampReq(cmd.ID)})
			// A shell pane is not an agent: text for one is a command line.
			cmd.AllowShell = false
		}
		s.guardFor(c, "handling "+cmdName(cmd.Cmd), func() { s.handleCommand(c, cmd) })
	}
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// clampReq (and clampCmd) cut what the device sent that is sent back, so that a
// refusal is not a way to make the desk write what it is given at length.
func clampReq(s string) string { return strings.ToValidUTF8(cutAt(s, 64), "") }

// clampSealedCmd is clampCmd with room for the longest command name the sealed
// socket runs (structuredNewConversation is 25 characters), so that a refusal
// names the command whole.
func clampSealedCmd(name string) string {
	const limit = 40
	if r := []rune(name); len(r) > limit {
		return strings.ToValidUTF8(string(r[:limit]), "") + "…"
	}
	return strings.ToValidUTF8(name, "")
}

// redactScrubbed redacts what a scrub left, and passes on its choice to send
// nothing.
func redactScrubbed(out []byte, strip []string) []byte {
	if out == nil {
		return nil
	}
	return redactSealed(out, strip)
}
