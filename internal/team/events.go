package team

import (
	"bytes"
	"encoding/json"
	"sync"
	"time"
)

// Kinds of Event.
const (
	// EventMessage: a message was sent from one pane to another.
	EventMessage = "message"
	// EventDelivery: a message reached a delivery state.
	EventDelivery = "delivery"
	// EventTap: a Claude session's SendMessage call was seen in its hook.
	EventTap = "tap"
)

const (
	// MaxEvents is the most events the log keeps.
	MaxEvents = 2000
	// MaxEventBytes is the most the log holds in bytes; the oldest lines are
	// dropped first when it is over.
	MaxEventBytes = 256 * 1024
)

// Event is one line of the team event log. It never holds message text: a
// message is named by its id, and the text stays in the inbox.
type Event struct {
	Kind string    `json:"kind"`
	ID   int       `json:"id"`
	Time time.Time `json:"time"`
	// From and To are the panes of a message event. From is empty when the
	// person or Flockdeck sent it. For a tap event From is the pane that made
	// the call and To the pane its recipient resolved to, empty when none did.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// State is the delivery state of a delivery event.
	State string `json:"state,omitempty"`
	// Tool is the tool name of a tap event, and State is "sent" for a call that
	// was read or "unread" for one that was seen and not understood. A tap has
	// no message id (ID is 0) and, like every event, no text.
	Tool string `json:"tool,omitempty"`
}

// Tap states.
const (
	TapSent   = "sent"
	TapUnread = "unread"
)

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
	l.trim()
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
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		switch e.Kind {
		case EventMessage, EventDelivery:
			if e.ID <= 0 {
				continue
			}
		case EventTap:
			// A tap names no message. Its tool name is cut as when it was made.
			e.ID, e.Tool = 0, CleanTool(e.Tool)
			if e.State != TapSent && e.State != TapUnread {
				continue
			}
		default:
			continue
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
	l.trim()
}
