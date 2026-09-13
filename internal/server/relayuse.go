package server

import (
	"sync"
	"time"
)

// relayUse is when each pane was last used from a window reached through the
// relay: its terminal opened or closed there, typed into, focused or tapped.
// A pane somebody has just been using from their phone is one they have seen
// waiting, and is not pushed to that phone as well; see pushDue.
//
// It is a time rather than the windows themselves. A phone put in a pocket
// keeps its socket open for a while, or drops it at once, depending on the
// browser, and neither is somebody looking; what counts is having used the
// pane lately.
var relayUse = &paneUse{at: map[string]time.Time{}}

type paneUse struct {
	mu sync.Mutex
	at map[string]time.Time
}

// mark records that a pane was used at t.
func (u *paneUse) mark(pane string, t time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if t.After(u.at[pane]) {
		u.at[pane] = t
	}
}

// since reports whether a pane was used at t or later. A use older than that
// is forgotten, since what is asked only moves on.
func (u *paneUse) since(pane string, t time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	at, ok := u.at[pane]
	if !ok {
		return false
	}
	if at.Before(t) {
		delete(u.at, pane)
		return false
	}
	return true
}
