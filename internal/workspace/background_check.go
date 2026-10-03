package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Background work an idle agent has left counted is checked against the
// conversation Claude Code has stored for the pane, so a count stuck on work
// that ended where no hook saw it corrects itself -- and only on evidence that
// it ended, never on a guess:
//
//   - a <task-notification> naming the task, which Claude Code records in the
//     conversation as a user turn when a background command or subagent ends;
//   - a KillShell/TaskStop call naming it that the tool answered without an
//     error;
//   - for a subagent run in the foreground, its Agent call returning (the
//     subagent's own meta.json, beside the conversation, names that call).
//
// The only sign of a subagent still running that is read is its own
// transcript being written to within backgroundSubagentFresh. A background
// command has none to read: Claude Code keeps its output outside the
// conversation's folder and names no process. Claude Code's own list at the
// end of each turn (hooks, Stop) stays the freshest word on both.
const (
	// BackgroundCheckInterval is how soon an idle pane with background work
	// counted is first checked, and how often while checks keep finding
	// something; a check that finds nothing doubles it, up to
	// backgroundCheckMaxInterval.
	BackgroundCheckInterval    = 45 * time.Second
	backgroundCheckMaxInterval = 8 * time.Minute
	// backgroundScanFirst is how much of the end of a conversation the first
	// check reads; later checks read only what has been added since, at most
	// backgroundScanStep at a time.
	backgroundScanFirst = 8 << 20
	backgroundScanStep  = 4 << 20
	// backgroundSubagentFresh is how recently a subagent's transcript must
	// have been written to for that to count as it still running.
	backgroundSubagentFresh = 2 * time.Minute
	// maxBackgroundCalls bounds the tool calls a check remembers across the
	// conversation; past it they are forgotten and a call answered after
	// that is not taken as evidence.
	maxBackgroundCalls = 4096
)

// backgroundCheck is what is remembered of one pane's checks.
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

// backgroundScan is what the conversation has said so far that a later line
// may complete: the KillShell/TaskStop calls not yet answered (tool use id to
// the task it stops), the foreground Agent calls not yet answered, and the
// Agent calls that have returned.
type backgroundScan struct {
	stops    map[string]string
	agents   map[string]bool
	returned map[string]bool
}

// backgroundEvidence is one thing a check found.
type backgroundEvidence struct {
	// id is the task or agent id it is about, with no kind.
	id string
	// ended says it shows the work has ended; otherwise it shows it running,
	// as of at.
	ended bool
	at    time.Time
	what  string
}

// backgroundChecks guards Workspace.bgChecks.
var backgroundChecksMu sync.Mutex

// BackgroundJob is one pane's check, worked out on the workspace goroutine
// by DueBackgroundChecks and run off it by RunBackgroundCheck.
type BackgroundJob struct {
	PaneID string
	sess   *session.Session
	path   string
	state  *backgroundCheck
}

// DueBackgroundChecks refreshes which counted background work is unverified on
// every agent pane (no reading of files), and returns the checks due at now:
// panes that are idle with background work counted and a stored Claude Code
// conversation to read. It runs on the workspace goroutine.
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
		if len(work) == 0 || st != session.StatusIdle {
			delete(w.bgChecks, p.ID)
			continue
		}
		c := w.bgChecks[p.ID]
		if c == nil {
			c = &backgroundCheck{interval: BackgroundCheckInterval, next: now.Add(BackgroundCheckInterval)}
			w.bgChecks[p.ID] = c
		}
		ids := make([]string, len(work))
		for i, b := range work {
			ids[i] = b.ID
		}
		if seen := strings.Join(ids, ","); seen != c.seen {
			// New work: back to checking at the base interval.
			if c.seen != "" && c.interval != BackgroundCheckInterval {
				c.interval = BackgroundCheckInterval
				c.next = now.Add(BackgroundCheckInterval)
			}
			c.seen = seen
		}
		if c.running || now.Before(c.next) {
			continue
		}
		spec, ok := w.specFor(p.Root, p.Agent)
		if !ok {
			continue
		}
		reader := transcript.For(spec)
		if _, claude := reader.(transcript.Claude); !claude {
			continue
		}
		path := reader.Path(spec, w.conversationOf(p))
		if path == "" {
			c.next = now.Add(c.interval)
			continue
		}
		c.running = true
		jobs = append(jobs, BackgroundJob{PaneID: p.ID, sess: p.Sess, path: path, state: c})
	}
	for id := range w.bgChecks {
		if !live[id] {
			delete(w.bgChecks, id)
		}
	}
	return jobs
}

// RunBackgroundCheck reads what job's conversation has added since its last
// check, and the transcripts of the subagents counted, and applies what they
// show: work shown to have ended stops being counted, with the evidence kept
// for the header; work shown running is confirmed. It reads nothing outside
// the conversation's own file and its folder, and can run on any goroutine.
func (w *Workspace) RunBackgroundCheck(job BackgroundJob, now time.Time) {
	c := job.state
	backgroundChecksMu.Lock()
	if c.path != job.path {
		*c = backgroundCheck{running: true, path: job.path, interval: c.interval, seen: c.seen}
	}
	offset, scan := c.offset, c.scan
	backgroundChecksMu.Unlock()

	found, offset := scanConversationFile(job.path, offset, &scan)
	found = append(found, checkSubagents(job.path, job.sess.BackgroundWork(), &scan, now)...)

	useful := false
	for _, e := range found {
		if e.ended {
			for _, b := range job.sess.BackgroundWork() {
				if bareBackgroundID(b.ID) == e.id {
					slog.Debug("background work ended by evidence", "pane", job.PaneID, "work", b.ID, "evidence", e.what)
					job.sess.EndBackgroundWork(b.ID, e.what)
					useful = true
				}
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
// offset has been replaced and is read again from the start.
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
	skipPartial := false
	if size < offset {
		offset, *scan = 0, backgroundScan{}
	}
	if offset == 0 && size > backgroundScanFirst {
		offset, skipPartial = size-backgroundScanFirst, true
	}
	// The first read takes the whole of the end it starts from, so the
	// latest of the conversation is read at once rather than a check later.
	limit := int64(backgroundScanStep)
	if skipPartial {
		limit = backgroundScanFirst
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
	// Only whole lines: one still being written is read next time.
	end := bytes.LastIndexByte(buf, '\n')
	if end < start {
		return nil, offset + int64(start)
	}
	return scanConversation(buf[start:end+1], scan), offset + int64(end+1)
}

// conversationLine is the part of a stored conversation line a check reads.
type conversationLine struct {
	Type    string `json:"type"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
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
	ShellID         string `json:"shell_id"`
	BashID          string `json:"bash_id"`
	TaskID          string `json:"task_id"`
	RunInBackground bool   `json:"run_in_background"`
}

// scanConversation reads whole conversation lines for the evidence described
// at the top of this file. A line that is not JSON is skipped.
func scanConversation(data []byte, scan *backgroundScan) []backgroundEvidence {
	if scan.stops == nil {
		scan.stops, scan.agents, scan.returned = map[string]string{}, map[string]bool{}, map[string]bool{}
	}
	var found []backgroundEvidence
	for _, raw := range bytes.Split(data, []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var line conversationLine
		if json.Unmarshal(raw, &line) != nil || (line.Type != "user" && line.Type != "assistant") {
			continue
		}
		var text string
		var blocks []conversationBlock
		if json.Unmarshal(line.Message.Content, &text) != nil {
			_ = json.Unmarshal(line.Message.Content, &blocks)
		}
		if line.Type == "user" {
			if strings.HasPrefix(strings.TrimSpace(text), "<task-notification>") {
				for _, id := range notificationTaskIDs(text) {
					found = append(found, backgroundEvidence{id: id, ended: true, what: "ended: its task notification is in the conversation"})
				}
			}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if strings.HasPrefix(strings.TrimSpace(b.Text), "<task-notification>") {
						for _, id := range notificationTaskIDs(b.Text) {
							found = append(found, backgroundEvidence{id: id, ended: true, what: "ended: its task notification is in the conversation"})
						}
					}
				case "tool_result":
					if id, ok := scan.stops[b.ToolUseID]; ok {
						delete(scan.stops, b.ToolUseID)
						if !b.IsError {
							found = append(found, backgroundEvidence{id: id, ended: true, what: "ended: stopped with TaskStop/KillShell in the conversation"})
						}
					}
					if scan.agents[b.ToolUseID] {
						delete(scan.agents, b.ToolUseID)
						if len(scan.returned) < maxBackgroundCalls {
							scan.returned[b.ToolUseID] = true
						}
					}
				}
			}
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_use" || b.ID == "" {
				continue
			}
			var in stopInput
			_ = json.Unmarshal(b.Input, &in)
			switch b.Name {
			case "TaskStop", "KillShell":
				id := in.TaskID
				for _, s := range []string{in.ShellID, in.BashID} {
					if id == "" {
						id = s
					}
				}
				if id != "" && len(scan.stops) < maxBackgroundCalls {
					scan.stops[b.ID] = id
				}
			case "Agent", "Task":
				// A background subagent's call returns as soon as it has
				// started, so only a foreground one's return says it ended.
				if !in.RunInBackground && len(scan.agents) < maxBackgroundCalls {
					scan.agents[b.ID] = true
				}
			}
		}
	}
	return found
}

// notificationTaskIDs is every <task-id> in a <task-notification>.
func notificationTaskIDs(text string) []string {
	var ids []string
	for {
		_, rest, ok := strings.Cut(text, "<task-id>")
		if !ok {
			return ids
		}
		id, after, ok := strings.Cut(rest, "</task-id>")
		if !ok {
			return ids
		}
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
		text = after
	}
}

// checkSubagents looks at the transcript Claude Code keeps of each counted
// subagent, beside the conversation at <conversation>/subagents/: one whose
// meta.json names an Agent call the conversation shows returning has ended,
// and one whose transcript was written within backgroundSubagentFresh is
// running.
func checkSubagents(conversation string, work []session.BackgroundWork, scan *backgroundScan, now time.Time) []backgroundEvidence {
	dir := filepath.Join(strings.TrimSuffix(conversation, ".jsonl"), "subagents")
	var found []backgroundEvidence
	for _, b := range work {
		kind, id, _ := strings.Cut(b.ID, ":")
		if kind != "agent" || id == "" || strings.ContainsAny(id, `/\.`) {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(dir, "agent-"+id+".meta.json")); err == nil {
			var meta struct {
				ToolUseID string `json:"toolUseId"`
			}
			if json.Unmarshal(data, &meta) == nil && meta.ToolUseID != "" && scan.returned[meta.ToolUseID] {
				found = append(found, backgroundEvidence{id: id, ended: true, what: "ended: its Agent call returned in the conversation"})
				continue
			}
		}
		if fi, err := os.Stat(filepath.Join(dir, "agent-"+id+".jsonl")); err == nil {
			if at := fi.ModTime(); now.Sub(at) < backgroundSubagentFresh {
				found = append(found, backgroundEvidence{id: id, at: at, what: "its transcript is being written"})
			}
		}
	}
	return found
}
