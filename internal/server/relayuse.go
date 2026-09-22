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

// forget removes any record of pane, called once the pane itself has closed:
// since is only ever asked about panes still open, iterated during a relay
// push, a closed pane's entry would otherwise sit in at for the life of the
// process. A pane never touched from the relay has nothing to remove, which
// is not an error.
func (u *paneUse) forget(pane string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.at, pane)
}

// has reports whether relayUse currently holds a record for pane, for tests
// to confirm a closed pane's entry does not linger.
func (u *paneUse) has(pane string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, ok := u.at[pane]
	return ok
}

// PaneClosed removes any relay-use record and inbox preview for a pane once
// it has closed. It is installed as the workspace's pane-closed hook (see
// Workspace.SetPaneClosedHook) so neither relayUse nor previewCache outlives
// the pane it describes.
func (s *Server) PaneClosed(paneID string) {
	relayUse.forget(paneID)
	s.preview.drop(paneID)
}
