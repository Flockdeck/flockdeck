package server

import "sync"

// This file is what lets a window at the desk see that a phone -- its own
// owner's, or a colleague's -- has a pane open right now, in its chat view or
// its terminal, and whose phone it is. Without it, text could appear in a
// pane, or a reply arrive in its chat, with nothing at the desk saying it
// came from a device reached through the relay rather than out of nowhere.
//
// Two things already know who has a pane open: the conversation hub's own
// watchers (conversation.go) for the chat view, and, new here, a small
// registry of open terminal sockets for the pane view. remoteViewersFor
// merges what both say into the device names a pane's header shows.

// remoteChatViewers lists the device names of every window reached through
// the relay that currently has paneID's chat view open. A pane nobody has
// ever opened, or that nobody has open now, answers nil.
func (h *conversationHub) remoteChatViewers(paneID string) []string {
	pc, ok := h.lookup(paneID)
	if !ok {
		return nil
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	var out []string
	for c := range pc.watchers {
		if c.remote {
			out = append(out, c.device)
		}
	}
	return out
}

// remoteTermViewers is every pane's terminal sockets reached through the
// relay that are open right now, keyed by the viewer id handlePTY already
// hands out (see nextViewer) so that two sockets on the same device -- a
// phone reconnecting before its old socket has timed out -- are each their
// own entry until one of them closes.
var remoteTermViewers = &remoteTermViewerSet{panes: map[string]map[int64]remoteTermViewer{}}

// remoteTermViewer is what is known of one terminal socket reached through
// the relay: the device it belongs to, and whether handlePTY's handshake
// end-to-end encrypted it (internal/e2e) -- false for an older browser that
// has not registered a key, which is served rather than refused, but whose
// pane a desk may still want to know is carrying its traffic in the clear.
type remoteTermViewer struct {
	device    string
	encrypted bool
}

type remoteTermViewerSet struct {
	mu    sync.Mutex
	panes map[string]map[int64]remoteTermViewer
}

// add records that a terminal socket reached through the relay is open on
// pane, on the device named, and whether it is end-to-end encrypted.
func (r *remoteTermViewerSet) add(pane string, viewer int64, device string, encrypted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.panes[pane]
	if m == nil {
		m = map[int64]remoteTermViewer{}
		r.panes[pane] = m
	}
	m[viewer] = remoteTermViewer{device: device, encrypted: encrypted}
}

// remove forgets a terminal socket that has closed.
func (r *remoteTermViewerSet) remove(pane string, viewer int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.panes[pane]
	delete(m, viewer)
	if len(m) == 0 {
		delete(r.panes, pane)
	}
}

// devices lists the device names with a terminal socket open on pane right
// now, one per socket -- so the same device open twice counts twice here,
// which remoteViewersFor's own de-duplication is what turns into one name.
func (r *remoteTermViewerSet) devices(pane string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.panes[pane]
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v.device)
	}
	return out
}

// insecure reports whether any terminal socket open on pane right now is
// not end-to-end encrypted -- a browser that predates the feature, or one
// this host could not complete a handshake's roster lookup for a moment
// ago. false when nobody reached through the relay has the pane's terminal
// open at all, the same as when every socket open on it is encrypted: there
// is nothing to warn about either way.
func (r *remoteTermViewerSet) insecure(pane string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.panes[pane] {
		if !v.encrypted {
			return true
		}
	}
	return false
}

// remoteViewersFor lists the device names of every window reached through the
// relay that has paneID open right now, in its chat view or its terminal --
// each name once, however many of either it has open, so a phone with both
// open does not appear to be two phones. Order follows first sight (chat
// before terminal) rather than being sorted, which is stable enough for a
// header naming one or two devices and cheaper than sorting on every
// snapshot. nil when nobody reached through the relay has the pane open.
func (s *Server) remoteViewersFor(paneID string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(names []string) {
		for _, n := range names {
			if seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	add(s.convos.remoteChatViewers(paneID))
	add(remoteTermViewers.devices(paneID))
	return out
}
