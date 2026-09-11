package server

import (
	"encoding/json"
	"net/http"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/store"
)

// The help pages are prose and are fetched over HTTP the first time they are
// asked for, because they are large, static and wanted rarely. The key table
// and the preferences are neither: the palette needs the table before the
// first keystroke, so both ride the control socket and arrive with the state.

// helloMsg is the first thing a window is sent. It carries what the interface
// needs before it can draw anything: the actions it offers, and what this
// person has already been shown.
type helloMsg struct {
	Type  string      `json:"type"`
	Keys  []help.Key  `json:"keys"`
	Prefs store.Prefs `json:"prefs"`
}

// sendHello gives a freshly connected window the key table and the prefs. It
// runs on the workspace goroutine, which is what owns s.prefs.
func (s *Server) sendHello(c *controlClient) {
	data, err := json.Marshal(helloMsg{Type: "hello", Keys: help.Keys, Prefs: s.prefs})
	if err != nil {
		return
	}
	c.send(data)
}

// prefsMsg tells every window that the preferences changed, so a second window
// does not go on offering a hint that was dismissed in the first.
type prefsMsg struct {
	Type  string      `json:"type"`
	Prefs store.Prefs `json:"prefs"`
}

// savePrefs persists the preferences and tells every window. It runs on the
// workspace goroutine.
func (s *Server) savePrefs() {
	if err := store.SavePrefs(s.prefs); err != nil {
		// Preferences are a convenience; failing to record one must not
		// interrupt what the person was actually doing.
		return
	}
	data, err := json.Marshal(prefsMsg{Type: "prefs", Prefs: s.prefs})
	if err != nil {
		return
	}
	s.mu.Lock()
	clients := make([]*controlClient, 0, len(s.clients))
	for cl := range s.clients {
		clients = append(clients, cl)
	}
	s.mu.Unlock()
	for _, cl := range clients {
		cl.send(data)
	}
}

// markHelpSeen records that the help has been opened, so the first-run welcome
// does not come back.
func (s *Server) markHelpSeen() {
	s.do(func() {
		if s.prefs.HelpSeen {
			return
		}
		s.prefs.HelpSeen = true
		s.savePrefs()
	})
}

// dismissTip records that an inline hint has been sent away for good.
func (s *Server) dismissTip(id string) {
	s.do(func() {
		if s.prefs.Dismiss(id) {
			s.savePrefs()
		}
	})
}

// handleHelp serves the rendered help pages to an authorised window.
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pages, err := help.Pages()
	if err != nil {
		http.Error(w, "the help pages could not be rendered", http.StatusInternalServerError)
		return
	}
	data, err := json.Marshal(map[string]any{"pages": pages})
	if err != nil {
		http.Error(w, "the help pages could not be encoded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// The content is compiled in, so it cannot change while this process runs.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}
