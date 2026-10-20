package team

import (
	"strings"
	"sync"
	"time"
	"unicode"
)

// A Tap is a SendMessage call one Claude session made to another, seen in the
// hook reply of the sending pane. Flockdeck does not carry these messages and
// cannot say whether the other session read one, so a tap is only ever drawn as
// sent. It holds no message text: the call is named by who, to whom and when.
type Tap struct {
	Time time.Time
	// From is the pane that made the call.
	From string
	// To is the pane the recipient name resolved to, or "" when it did not
	// resolve to exactly one pane. Always "" when Understood is false.
	To string
	// Tool is the tool name as the hook reported it, cut to MaxTapTool.
	Tool string
	// Understood is false for a call to a SendMessage tool whose input the
	// parser could not read a recipient and a message from. Only the tool name
	// and the sender are kept of it.
	Understood bool
}

const (
	// MaxTaps is how many taps a TapLog keeps, oldest dropped first.
	MaxTaps = 200
	// MaxTapTool is the most characters of a tool name a tap keeps.
	MaxTapTool = 64
)

// TapLog is the last taps seen. It is safe for concurrent use and is kept in
// memory only.
type TapLog struct {
	mu   sync.Mutex
	taps []Tap
	rev  int
}

// Add records a tap, dropping the oldest over MaxTaps.
func (l *TapLog) Add(t Tap) {
	t.Tool = cleanTool(t.Tool)
	if !t.Understood {
		t.To = ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.taps = append(l.taps, t)
	if n := len(l.taps) - MaxTaps; n > 0 {
		l.taps = append([]Tap(nil), l.taps[n:]...)
	}
	l.rev++
}

// Taps returns the taps held, oldest first.
func (l *TapLog) Taps() []Tap {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Tap(nil), l.taps...)
}

// Rev changes whenever a tap is added.
func (l *TapLog) Rev() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rev
}

// cleanTool is a tool name with control characters removed, cut to MaxTapTool
// characters.
func cleanTool(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range name {
		if unicode.IsControl(r) {
			continue
		}
		if n == MaxTapTool {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
