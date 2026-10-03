package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Background work an idle agent has left counted is checked against the
// conversation Claude Code has stored for the pane, so a count stuck on work
// that ended where no hook saw it corrects itself -- and only on evidence that
// it ended, never on a guess. What counts, as Claude Code 2.1.28x records it:
//
//   - a <task-notification> naming the task with a status that says it has
//     stopped ("completed", "stopped", ...), whether recorded as a user turn
//     or, when it was folded into a turn already going, as a queued_command
//     attachment;
//   - a subagent's hand-back: a message whose origin is the subagent
//     (senderTaskId) and says it is its hand-back;
//   - a TaskStop/KillShell naming the task that the tool answered without an
//     error;
//   - a subagent's own transcript ending on the turn-ending tool result
//     (toolEndsTurn) of its hand-back;
//   - a non-persistent Monitor past the timeoutMs it was started with, at
//     which Claude Code stops it (strong evidence rather than a record of the
//     end, dated when the timeout ran out).
//
// Each piece carries the time Claude Code wrote it, and ends the work only
// when that is after Flockdeck first heard of the work and after the latest
// sign it was running (see session.EndBackgroundWorkSeen): an old end in the
// history is about an earlier task, not this one.
//
// An Agent call's result is not an end: Claude Code runs every subagent
// asynchronously and answers the call at once ("async_launched").
//
// Signs of running: a subagent's transcript, or a task's output file, written
// to within backgroundFresh. Claude Code's own list at the end of each turn
// (hooks, Stop) stays the freshest word on all of it.
const (
	// BackgroundCheckInterval is how soon an idle pane with background work
	// counted is first checked, and how often while checks keep finding
	// something; a check that finds nothing doubles it, up to
	// backgroundCheckMaxInterval.
	BackgroundCheckInterval    = 45 * time.Second
	backgroundCheckMaxInterval = 8 * time.Minute
	// backgroundScanFirst is how much of the end of a conversation the first
	// read takes; later reads take only what has been added since, at most
	// backgroundScanStep at a time.
	backgroundScanFirst = 8 << 20
	backgroundScanStep  = 4 << 20
	// backgroundTailBytes is how much of the end of a subagent's transcript
	// is read for its last line.
	backgroundTailBytes = 256 << 10
	// backgroundFresh is how recently a subagent's transcript or a task's
	// output must have been written to for that to count as it running.
	backgroundFresh = 2 * time.Minute
	// maxBackgroundStops bounds the TaskStop/KillShell calls a check keeps
	// waiting for an answer to; past it, one is not taken as evidence.
	maxBackgroundStops = 1024
)

// backgroundTaskID is the shape of an id Claude Code gives a background task
// or subagent. Anything else is never put into a path.
var backgroundTaskID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// backgroundStopped are the <status> values of a task notification that say
// the task is no longer running.
var backgroundStopped = map[string]bool{
	"completed": true, "stopped": true, "killed": true, "failed": true, "cancelled": true, "canceled": true, "error": true,
}

// backgroundCheck is what is remembered of one pane's checks. It is kept for
// as long as the pane is open, so a pane that goes busy and idle again goes on
// reading its conversation where it left off.
type backgroundCheck struct {
	running  bool
	path     string
	offset   int64
	next     time.Time
	interval time.Duration
	// seen is the work counted at the last check, so new work resets the
	// backoff.
	seen string
	scan backgroundScan
}

// backgroundScan is the TaskStop/KillShell calls the conversation has made and
// not yet had answered: tool use id to the task it stops.
type backgroundScan struct {
	stops map[string]string
	// monitors is when each non-persistent Monitor the conversation started
	// runs out: its result's time plus the timeoutMs it was started with,
	// after which Claude Code stops it.
	monitors map[string]time.Time
}

// backgroundEvidence is one thing a check found.
type backgroundEvidence struct {
	// id is the task or agent id it is about, with no kind.
	id string
	// ended says it shows the work has ended; otherwise it shows it running.
	// Either way, at is when.
	ended bool
	at    time.Time
	what  string
}

// backgroundChecksMu guards Workspace.bgChecks and what it holds.
var backgroundChecksMu sync.Mutex

// BackgroundJob is one pane's check, worked out on the workspace goroutine
// by DueBackgroundChecks and run off it by RunBackgroundCheck.
type BackgroundJob struct {
	PaneID string
	sess   *session.Session
	spec   agent.Spec
	conv   string
	state  *backgroundCheck
}

// DueBackgroundChecks refreshes which counted background work is unverified on
// every agent pane (no reading of files), and returns the checks due at now:
// agent panes that are idle with background work counted and run Claude Code.
// It runs on the workspace goroutine and touches no file; finding the
// conversation is left to RunBackgroundCheck.
func (w *Workspace) DueBackgroundChecks(now time.Time) []BackgroundJob {
	w.mu.RLock()
	panes := make([]*Pane, 0, len(w.panes))
	for _, p := range w.panes {
		panes = append(panes, p)
	}
	w.mu.RUnlock()
	sort.Slice(panes, func(i, j int) bool { return panes[i].ID < panes[j].ID })

	backgroundChecksMu.Lock()
	defer backgroundChecksMu.Unlock()
	if w.bgChecks == nil {
		w.bgChecks = map[string]*backgroundCheck{}
	}
	live := map[string]bool{}
	var jobs []BackgroundJob
	for _, p := range panes {
		if p.Sess == nil || !p.IsAgent() {
			continue
		}
		live[p.ID] = true
		p.Sess.RefreshBackground(now)
		work := p.Sess.BackgroundWork()
		st, _ := p.Sess.Status()
		c := w.bgChecks[p.ID]
		if len(work) == 0 || st != session.StatusIdle {
			// Not due, but what has been read is remembered: the next idle
			// spell carries on from it rather than reading the history again.
			if c != nil {
				c.seen = ""
			}
			continue
		}
		if c == nil {
			c = &backgroundCheck{interval: BackgroundCheckInterval}
			w.bgChecks[p.ID] = c
		}
		ids := make([]string, len(work))
		for i, b := range work {
			ids[i] = b.ID
		}
		if seen := strings.Join(ids, ","); seen != c.seen {
			// New work, or a new idle spell: back to the base interval.
			c.interval = BackgroundCheckInterval
			c.next = now.Add(BackgroundCheckInterval)
			c.seen = seen
		}
		if c.running || now.Before(c.next) {
			continue
		}
		spec, ok := w.specFor(p.Root, p.Agent)
		if !ok {
			continue
		}
		if _, claude := transcript.For(spec).(transcript.Claude); !claude {
			continue
		}
		c.running = true
		jobs = append(jobs, BackgroundJob{PaneID: p.ID, sess: p.Sess, spec: spec, conv: w.conversationOf(p), state: c})
	}
	for id := range w.bgChecks {
		if !live[id] {
			delete(w.bgChecks, id)
		}
	}
	return jobs
}

// RunBackgroundCheck reads what job's conversation has added since its last
// check, and the transcripts and output files of the work counted, and applies
// what they show: work shown to have ended since it was last seen running
// stops being counted, with the evidence kept for the header; work shown
// running is confirmed. It can run on any goroutine.
func (w *Workspace) RunBackgroundCheck(job BackgroundJob, now time.Time) {
	c := job.state
	path := transcript.For(job.spec).Path(job.spec, job.conv)

	backgroundChecksMu.Lock()
	if c.path != path {
		c.path, c.offset, c.scan = path, 0, backgroundScan{}
	}
	offset, scan := c.offset, c.scan
	backgroundChecksMu.Unlock()

	var found []backgroundEvidence
	if path != "" {
		found, offset = scanConversationFile(path, offset, &scan)
		found = append(found, checkWorkFiles(path, os.TempDir(), job.sess.BackgroundWork(), now)...)
		found = append(found, monitorsTimedOut(&scan, now)...)
	}

	useful := false
	for _, e := range found {
		if e.ended {
			if job.sess.EndBackgroundWorkSeen(e.id, e.at, e.what) {
				slog.Debug("background work ended by evidence", "pane", job.PaneID, "task", e.id, "evidence", e.what, "at", e.at)
				useful = true
			}
			continue
		}
		for _, b := range job.sess.BackgroundWork() {
			if bareBackgroundID(b.ID) == e.id && job.sess.ConfirmBackgroundWork(b.ID, e.at, e.what) {
				useful = true
			}
		}
	}
	job.sess.RefreshBackground(now)

	backgroundChecksMu.Lock()
	c.offset, c.scan, c.running = offset, scan, false
	if useful {
		c.interval = BackgroundCheckInterval
	} else if c.interval *= 2; c.interval > backgroundCheckMaxInterval {
		c.interval = backgroundCheckMaxInterval
	}
	c.next = now.Add(c.interval)
	backgroundChecksMu.Unlock()
}

// bareBackgroundID is a counted id without its kind.
func bareBackgroundID(id string) string {
	if _, bare, ok := strings.Cut(id, ":"); ok {
		return bare
	}
	return id
}

// scanConversationFile reads the conversation at path from offset -- or, the
// first time (offset 0), its last backgroundScanFirst bytes -- and returns
// what it shows and the offset to start from next time. A file shorter than
// offset has been replaced and is read again from the start. Only whole lines
// are read; one longer than a whole read is skipped.
func scanConversationFile(path string, offset int64, scan *backgroundScan) ([]backgroundEvidence, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, offset
	}
	size := fi.Size()
	if size < offset {
		offset, *scan = 0, backgroundScan{}
	}
	limit := int64(backgroundScanStep)
	skipPartial := false
	if offset == 0 {
		limit = backgroundScanFirst
		if size > backgroundScanFirst {
			offset, skipPartial = size-backgroundScanFirst, true
		}
	}
	n := size - offset
	if n > limit {
		n = limit
	}
	if n <= 0 {
		return nil, offset
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, offset
	}
	start := 0
	if skipPartial {
		// Started in the middle of a line: begin at the next whole one.
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return nil, offset + n
		}
		start = i + 1
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < start {
		if n == limit {
			// A line longer than a whole read: skip what was read of it
			// rather than reading the same bytes every check.
			return nil, offset + n
		}
		// A line still being written: read it next time.
		return nil, offset + int64(start)
	}
	return scanConversation(buf[start:end+1], scan), offset + int64(end+1)
}

// conversationLine is the part of a stored conversation line a check reads.
type conversationLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Origin    *lineOrigin     `json:"origin"`
	Message   lineMessage     `json:"message"`
	Attach    *lineAttachment `json:"attachment"`
	// Result is a tool result's toolUseResult; only a Monitor's is read.
	Result json.RawMessage `json:"toolUseResult"`
}

type lineMessage struct {
	Content json.RawMessage `json:"content"`
}

type lineAttachment struct {
	Type        string      `json:"type"`
	CommandMode string      `json:"commandMode"`
	Prompt      string      `json:"prompt"`
	Origin      *lineOrigin `json:"origin"`
}

// lineOrigin is where a user turn or queued command came from. A subagent's
// hand-back names it as senderTaskId and says handback.
type lineOrigin struct {
	Kind         string `json:"kind"`
	SenderTaskID string `json:"senderTaskId"`
	Handback     bool   `json:"handback"`
}

type conversationBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

type stopInput struct {
	ShellID string `json:"shell_id"`
	BashID  string `json:"bash_id"`
	TaskID  string `json:"task_id"`
}

// lineTime is a conversation line's timestamp, or the zero time, which is
// never taken as evidence of an end.
func lineTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// scanConversation reads whole conversation lines for the evidence described
// at the top of this file. A line that is not JSON is skipped.
func scanConversation(data []byte, scan *backgroundScan) []backgroundEvidence {
	if scan.stops == nil {
		scan.stops = map[string]string{}
	}
	if scan.monitors == nil {
		scan.monitors = map[string]time.Time{}
	}
	var found []backgroundEvidence
	ended := func(id string, at time.Time, what string) {
		if id != "" {
			found = append(found, backgroundEvidence{id: id, ended: true, at: at, what: what})
		}
	}
	notifications := func(text string, at time.Time) {
		for _, n := range taskNotifications(text) {
			if backgroundStopped[n.status] {
				ended(n.id, at, "ended: its task notification ("+n.status+") is in the conversation")
			}
		}
	}
	handback := func(o *lineOrigin, at time.Time) {
		if o != nil && o.Handback && o.SenderTaskID != "" {
			ended(o.SenderTaskID, at, "ended: its hand-back is in the conversation")
		}
	}
	for _, raw := range bytes.Split(data, []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var line conversationLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		at := lineTime(line.Timestamp)
		switch line.Type {
		case "attachment":
			// A notification or hand-back that arrived while a turn was going
			// is folded into it, and recorded only as this.
			if a := line.Attach; a != nil && a.Type == "queued_command" {
				if a.CommandMode == "task-notification" {
					notifications(a.Prompt, at)
				}
				handback(a.Origin, at)
			}
			continue
		case "user", "assistant":
		default:
			continue
		}
		var text string
		var blocks []conversationBlock
		if json.Unmarshal(line.Message.Content, &text) != nil {
			_ = json.Unmarshal(line.Message.Content, &blocks)
		}
		if line.Type == "user" {
			handback(line.Origin, at)
			monitorStarted(scan, line.Result, at)
			// Only a turn Claude Code itself marks as a notification is read
			// as one. Typed or pasted text can say anything.
			fromClaude := line.Origin != nil && line.Origin.Kind == "task-notification"
			if fromClaude {
				notifications(text, at)
			}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if fromClaude {
						notifications(b.Text, at)
					}
				case "tool_result":
					if id, ok := scan.stops[b.ToolUseID]; ok {
						delete(scan.stops, b.ToolUseID)
						if !b.IsError {
							ended(id, at, "ended: stopped with TaskStop/KillShell in the conversation")
						}
					}
				}
			}
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_use" || b.ID == "" || (b.Name != "TaskStop" && b.Name != "KillShell") {
				continue
			}
			var in stopInput
			_ = json.Unmarshal(b.Input, &in)
			id := in.TaskID
			for _, s := range []string{in.ShellID, in.BashID} {
				if id == "" {
					id = s
				}
			}
			if id != "" && len(scan.stops) < maxBackgroundStops {
				scan.stops[b.ID] = id
			}
		}
	}
	return found
}

// monitorWire is a Monitor call's toolUseResult, as Claude Code records it.
type monitorWire struct {
	TaskID     string `json:"taskId"`
	Persistent *bool  `json:"persistent"`
	TimeoutMs  int64  `json:"timeoutMs"`
}

// monitorStarted notes when a non-persistent Monitor started at at runs out.
func monitorStarted(scan *backgroundScan, raw json.RawMessage, at time.Time) {
	if len(raw) == 0 || at.IsZero() || len(scan.monitors) >= maxBackgroundStops {
		return
	}
	var m monitorWire
	if json.Unmarshal(raw, &m) != nil || m.TaskID == "" || m.Persistent == nil || *m.Persistent || m.TimeoutMs <= 0 {
		return
	}
	scan.monitors[m.TaskID] = at.Add(time.Duration(m.TimeoutMs) * time.Millisecond)
}

// monitorsTimedOut is an end for every monitor whose timeout has passed,
// dated when it ran out, and forgets it.
func monitorsTimedOut(scan *backgroundScan, now time.Time) []backgroundEvidence {
	var found []backgroundEvidence
	for id, until := range scan.monitors {
		if now.After(until) {
			found = append(found, backgroundEvidence{id: id, ended: true, at: until, what: "ended: its monitor timeout ran out"})
			delete(scan.monitors, id)
		}
	}
	return found
}

// taskNotification is the id and status of one <task-notification>.
type taskNotification struct{ id, status string }

// taskNotifications reads the one <task-notification> a message is. Its id
// and status are the first of each, which Claude Code writes ahead of the
// summary and result. A message holding a second opening tag is read as
// none: the summary or a monitor's event can carry text from anywhere, and a
// forged notification inside it must not end another task.
func taskNotifications(text string) []taskNotification {
	const open = "<task-notification>"
	body, ok := strings.CutPrefix(strings.TrimSpace(text), open)
	if !ok || strings.Contains(body, open) {
		return nil
	}
	id := strings.TrimSpace(between(body, "<task-id>", "</task-id>"))
	status := strings.ToLower(strings.TrimSpace(between(body, "<status>", "</status>")))
	if id == "" {
		return nil
	}
	return []taskNotification{{id: id, status: status}}
}

// between is the text of s between the first open and the close after it.
func between(s, open, close string) string {
	_, rest, ok := strings.Cut(s, open)
	if !ok {
		return ""
	}
	in, _, ok := strings.Cut(rest, close)
	if !ok {
		return ""
	}
	return in
}

// checkWorkFiles looks at the files Claude Code keeps of each piece of counted
// work, by its id and nowhere else:
//
//   - a subagent's own transcript, <conversation>/subagents/agent-<id>.jsonl:
//     ending on its hand-back's turn-ending result has it ended; written to
//     within backgroundFresh otherwise has it running;
//   - a task's output, <temp>/claude/<project folder>/<conversation>/tasks/
//     <id>.output, which Claude Code appends a background command's output
//     to: written to within backgroundFresh has it running.
func checkWorkFiles(conversation, temp string, work []session.BackgroundWork, now time.Time) []backgroundEvidence {
	convDir := strings.TrimSuffix(conversation, ".jsonl")
	subagents := filepath.Join(convDir, "subagents")
	tasks := ""
	if temp != "" {
		tasks = filepath.Join(temp, "claude", filepath.Base(filepath.Dir(conversation)), filepath.Base(convDir), "tasks")
	}
	var found []backgroundEvidence
	for _, b := range work {
		kind, id, _ := strings.Cut(b.ID, ":")
		if !backgroundTaskID.MatchString(id) {
			continue
		}
		if kind == "agent" || kind == "task" {
			p := filepath.Join(subagents, "agent-"+id+".jsonl")
			if at, ok := subagentEnded(p); ok {
				found = append(found, backgroundEvidence{id: id, ended: true, at: at, what: "ended: its transcript ends on its hand-back"})
				continue
			}
			if fi, err := os.Stat(p); err == nil && now.Sub(fi.ModTime()) < backgroundFresh {
				found = append(found, backgroundEvidence{id: id, at: fi.ModTime(), what: "its transcript is being written"})
				continue
			}
		}
		if tasks != "" {
			if fi, err := os.Stat(filepath.Join(tasks, id+".output")); err == nil && fi.Size() > 0 && now.Sub(fi.ModTime()) < backgroundFresh {
				found = append(found, backgroundEvidence{id: id, at: fi.ModTime(), what: "its output is being written"})
			}
		}
	}
	return found
}

// subagentEnded reads the last line of a subagent's transcript and reports
// when it was written if it is the tool result that ends the subagent's turn
// (toolEndsTurn), which Claude Code writes as a subagent hands back.
func subagentEnded(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return time.Time{}, false
	}
	off := fi.Size() - backgroundTailBytes
	if off < 0 {
		off = 0
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return time.Time{}, false
	}
	buf = bytes.TrimRight(buf, "\r\n ")
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		buf = buf[i+1:]
	} else if off > 0 {
		return time.Time{}, false
	}
	var last struct {
		Type         string `json:"type"`
		Timestamp    string `json:"timestamp"`
		ToolEndsTurn bool   `json:"toolEndsTurn"`
	}
	if json.Unmarshal(buf, &last) != nil || last.Type != "user" || !last.ToolEndsTurn {
		return time.Time{}, false
	}
	at := lineTime(last.Timestamp)
	return at, !at.IsZero()
}
