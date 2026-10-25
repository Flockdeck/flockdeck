package team

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Kinds of Event.
const (
	// EventMessage: a message was sent from one pane to another.
	EventMessage = "message"
	// EventDelivery: a message reached a delivery state.
	EventDelivery = "delivery"
	// EventWake: a line was typed into a pane to wake its agent for a message.
	EventWake = "wake"
	// EventRename: a pane was given a label by another pane. From is the pane that
	// did it, To the pane named, Old and New the labels (see MaxPaneLabel).
	EventRename = "rename"
)

const (
	// MaxEvents is the most events the log keeps.
	MaxEvents = 2000
	// MaxEventBytes is the most the log holds in bytes; the oldest lines are
	// dropped first when it is over.
	MaxEventBytes = 256 * 1024
	// MaxRenameEvents is the most rename events the log keeps, the newest. A burst of
	// renames cannot push the wake and delivery events out of the shared caps.
	MaxRenameEvents = 200
)

// Event is one line of the team event log. A message is named by its id and the
// text stays in the inbox. The one exception is a wake event in text mode (see
// MaxTextLine): its Line is the text that was typed, up to MaxTextLine
// characters, since that is what was typed.
type Event struct {
	Kind string    `json:"kind"`
	ID   int       `json:"id"`
	Time time.Time `json:"time"`
	// From and To are the panes of a message event. From is empty when the
	// person or Flockdeck sent it.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// State is the delivery state of a delivery event.
	State string `json:"state,omitempty"`

	// The rest belong to a wake event, which is the record of a line Flockdeck
	// typed into a pane. None of it is message text: ID names the message.
	//
	// Pane is the pane typed into and Agent its agent type. Rule is the wake
	// rule's version and RuleSource where the rule came from. Prompt is the
	// prompt line that was matched, control characters removed and cut to
	// MaxPromptLine characters, and Line is what was typed: the fixed line, or for
	// an agent the user opted in to text mode, the prefix and the message text
	// (see MaxTextLine). Only then does an event hold any of a message's text.
	Pane       string `json:"pane,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Rule       string `json:"rule,omitempty"`
	RuleSource string `json:"ruleSource,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Line       string `json:"line,omitempty"`
	// Unsubmitted is set when the line was typed but its Enter was not, so it
	// may still sit in the pane's input box and the message was not marked woken.
	Unsubmitted bool `json:"unsubmitted,omitempty"`
	// Old and New are the labels of a rename event, each at most MaxPaneLabel characters.
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// MaxPromptLine is the most characters of a matched prompt line a wake event
// keeps.
const MaxPromptLine = 160

// MaxTextLine is the most characters a text mode wake line has, and so the most
// of it a wake event keeps.
const MaxTextLine = 300

// maxWakeField is the most characters of any other text field of a wake event.
const maxWakeField = 64

// CleanLine removes control characters from line and cuts it to max characters.
func CleanLine(line string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range line {
		if unicode.IsControl(r) {
			continue
		}
		if n == max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// cleanWake applies the limits a wake event is written with, so that a file
// that was damaged or edited cannot bring long or odd fields back.
func cleanWake(e *Event) {
	e.Pane = CleanLine(e.Pane, maxWakeField)
	e.Agent = CleanLine(e.Agent, maxWakeField)
	e.Rule = CleanLine(e.Rule, maxWakeField)
	e.RuleSource = CleanLine(e.RuleSource, maxWakeField)
	e.Prompt = CleanLine(e.Prompt, MaxPromptLine)
	e.Line = CleanLine(e.Line, MaxTextLine)
}

// cleanRename applies the limits a rename event is written with.
func cleanRename(e *Event) {
	e.From = CleanLine(e.From, maxWakeField)
	e.To = CleanLine(e.To, maxWakeField)
	e.Old = CleanLine(e.Old, MaxPaneLabel)
	e.New = CleanLine(e.New, MaxPaneLabel)
}

// EventLog is the record of events kept for a project: one JSON line for each,
// oldest first, capped at MaxEvents lines and MaxEventBytes. It is safe for
// concurrent use. It does no file work of its own: Encode is what the caller
// saves, and Load reads it back.
type EventLog struct {
	mu    sync.Mutex
	lines [][]byte
	size  int
}

// Append adds an event, then drops the oldest lines until the log fits.
func (l *EventLog) Append(e Event) {
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
	l.size += len(line) + 1
	if e.Kind == EventRename {
		l.capRenames()
	}
	l.trim()
}

var renameMark = []byte(`"kind":"` + EventRename + `"`)

// capRenames drops the oldest rename events beyond MaxRenameEvents. l.mu is held.
func (l *EventLog) capRenames() {
	n := 0
	for _, line := range l.lines {
		if bytes.Contains(line, renameMark) {
			n++
		}
	}
	if n <= MaxRenameEvents {
		return
	}
	kept := make([][]byte, 0, len(l.lines))
	for _, line := range l.lines {
		if n > MaxRenameEvents && bytes.Contains(line, renameMark) {
			n--
			l.size -= len(line) + 1
			continue
		}
		kept = append(kept, line)
	}
	l.lines = kept
}

// trim drops lines from the front while the log is over a cap. l.mu is held.
func (l *EventLog) trim() {
	drop := 0
	for drop < len(l.lines) && (len(l.lines)-drop > MaxEvents || l.size > MaxEventBytes) {
		l.size -= len(l.lines[drop]) + 1
		drop++
	}
	if drop > 0 {
		l.lines = append([][]byte(nil), l.lines[drop:]...)
	}
}

// Events returns the events held, oldest first.
func (l *EventLog) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, 0, len(l.lines))
	for _, line := range l.lines {
		var e Event
		if json.Unmarshal(line, &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Encode is the log as it is saved: the lines, each ended by a newline.
func (l *EventLog) Encode() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) == 0 {
		return nil
	}
	return append(bytes.Join(l.lines, []byte("\n")), '\n')
}

// Load replaces the log with what Encode wrote. A line that is damaged, has an
// unknown kind or no id is skipped, so a half-written file still gives back
// what is good in it. The caps apply as for Append.
func (l *EventLog) Load(data []byte) {
	var lines [][]byte
	size := 0
	for _, raw := range bytes.Split(data, []byte("\n")) {
		var e Event
		if json.Unmarshal(raw, &e) != nil || (e.ID <= 0 && e.Kind != EventRename) || (e.Kind != EventMessage && e.Kind != EventDelivery && e.Kind != EventWake && e.Kind != EventRename) {
			continue
		}
		if e.Kind == EventRename {
			cleanRename(&e)
		}
		if e.Kind == EventWake {
			cleanWake(&e)
		}
		// Written again from the parsed event, so that a field the log does not
		// know, text among them, is not kept.
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		lines = append(lines, line)
		size += len(line) + 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines, l.size = lines, size
	l.capRenames()
	l.trim()
}
