package server

import (
	"encoding/json"
	"net/http"
	"strings"

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
	keys := help.Keys
	if c.remote {
		keys = remoteKeys
	}
	data, err := json.Marshal(helloMsg{Type: "hello", Keys: keys, Prefs: s.prefs})
	if err != nil {
		return
	}
	c.send(data)
}

// remoteKeys is the key table without what a window reached through the relay
// cannot do. Detach and Quit are the desk's to choose -- a phone's window
// closing never stopped anything, and nothing on the phone could start the
// agents again -- and the server refuses both from there. Offered anyway, Quit
// asked whether to stop every agent only for the answer to be no.
var remoteKeys = func() []help.Key {
	out := make([]help.Key, 0, len(help.Keys))
	for _, k := range help.Keys {
		switch k.ID {
		case "detach", "quit":
			continue
		}
		out = append(out, k)
	}
	return out
}()

// prefsMsg tells every window that the preferences changed, so a second window
// does not go on offering a hint that was dismissed in the first.
type prefsMsg struct {
	Type  string      `json:"type"`
	Prefs store.Prefs `json:"prefs"`
}

// updatePrefs applies one change to the preferences and, where it changed
// anything, saves them and tells every window.
//
// The change is made to what is on disk, not to the copy read at start-up.
// Written back whole, that copy undid whatever else had changed the file
// since: with two instances running (-solo) whichever saved last put back the
// other's old settings, and a start-up read that failed left defaults in
// memory which the first save then wrote over every setting there was.
func (s *Server) updatePrefs(change func(*store.Prefs) bool) {
	s.do(func() {
		p := store.LoadPrefs()
		if !change(&p) {
			return
		}
		if err := store.SavePrefs(p); err != nil {
			// Preferences are a convenience; failing to record one must not
			// interrupt what the person was actually doing.
			return
		}
		s.prefs = p
		s.broadcastPrefs()
	})
}

// broadcastPrefs tells every window what the preferences now are. It runs on
// the workspace goroutine.
func (s *Server) broadcastPrefs() {
	data, err := json.Marshal(prefsMsg{Type: "prefs", Prefs: s.prefs})
	if err != nil {
		return
	}
	for _, cl := range s.clientList() {
		cl.send(data)
	}
}

// markHelpSeen records that the help has been opened, so the first-run welcome
// does not come back.
func (s *Server) markHelpSeen() {
	s.updatePrefs(func(p *store.Prefs) bool {
		if p.HelpSeen {
			return false
		}
		p.HelpSeen = true
		return true
	})
}

// dismissTip records that an inline hint has been sent away for good.
func (s *Server) dismissTip(id string) {
	s.updatePrefs(func(p *store.Prefs) bool { return p.Dismiss(id) })
}

// setFontSize records the size the terminals are drawn at. The window used to
// keep it itself, in local storage, which forgot it on every run.
func (s *Server) setFontSize(size int) {
	if size < 8 || size > 28 {
		return
	}
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.FontSize, size) })
}

// setPref sets one preference and reports whether that changed it.
func setPref[T comparable](field *T, v T) bool {
	if *field == v {
		return false
	}
	*field = v
	return true
}

// setNotifications records whether the window raises desktop notifications.
// The browser's own answer cannot stand in for this: it belongs to the page's
// origin, which changes with the port on every run.
func (s *Server) setNotifications(off bool) {
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.NotificationsOff, off) })
}

// setScrollback records how many lines each terminal keeps, within what a
// window will accept.
func (s *Server) setScrollback(lines int) {
	if lines < 1000 || lines > 200000 {
		return
	}
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.Scrollback, lines) })
}

// setUpdates records whether the application checks for new releases, which
// could otherwise only be turned off with an environment variable.
func (s *Server) setUpdates(off bool) {
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.UpdatesOff, off) })
}

// resetTips brings back every inline hint that was dismissed, which could
// otherwise only be done by editing prefs.json.
func (s *Server) resetTips() {
	s.updatePrefs(func(p *store.Prefs) bool {
		if len(p.DismissedTips) == 0 {
			return false
		}
		p.DismissedTips = nil
		return true
	})
}

// setCursorSteady records whether the terminal cursors blink, which was fixed
// in the source.
func (s *Server) setCursorSteady(steady bool) {
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.CursorSteady, steady) })
}

// setCursorStyle records the shape of the terminal cursors. A block is the
// default and is kept as nothing, so a file written before there was a choice
// reads the same; a shape the terminals do not draw is refused.
func (s *Server) setCursorStyle(style string) {
	switch style {
	case "block":
		style = ""
	case "", "bar", "underline":
	default:
		return
	}
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.CursorStyle, style) })
}

// setFontFamily records the typeface the terminals are drawn in; empty goes
// back to the default. It arrives from a text field, so anything longer than
// a font list could sensibly be is refused rather than kept.
func (s *Server) setFontFamily(family string) {
	family = strings.TrimSpace(family)
	if len(family) > 200 {
		return
	}
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.FontFamily, family) })
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
	// Compiled in, so it cannot change while this process runs -- but a cache
	// outlives the process. Through the relay this address is the same from
	// one run to the next, so a window opened within the hour after an upgrade
	// was handed the last version's pages, naming keys and dialogs that had
	// since changed. It is fetched once per page load, over loopback or a link
	// that is carrying a whole terminal anyway.
	w.Header().Set("Cache-Control", "no-store")
	// A fifth of the size gzipped, for the reason the assets are: through the
	// relay the window is often a phone, and the first one opens the help
	// unasked.
	if fromRemote(r) && acceptsGzip(r) {
		if packed, ok := gzipped("/help.json", func() ([]byte, error) { return data, nil }); ok {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Vary", "Accept-Encoding")
			data = packed
		}
	}
	_, _ = w.Write(data)
}
