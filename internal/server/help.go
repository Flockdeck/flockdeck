package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/keybindings"
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
	Keys  []keyView   `json:"keys"`
	Prefs store.Prefs `json:"prefs"`
	// Remote says the window was reached through the relay, so that it can
	// leave out what only the desk may do: restart onto an update, turn
	// remote access off or on, set an API key. The snapshot cannot say it,
	// being one message broadcast to every window alike.
	Remote bool `json:"remote,omitempty"`
	// E2EPublicKey is this machine's own end-to-end public key (internal/e2e),
	// base64url, given only to a window reached through the relay: the "host"
	// side of the StartDeviceHandshake it runs for each terminal socket it
	// opens here. Left out for a local window, which needs nothing to
	// encrypt against itself, and for a remote one before this machine has
	// made an identity of its own yet.
	E2EPublicKey string `json:"e2ePublicKey,omitempty"`
}

// keyView is one action as a window is told of it: help.Key, with whether
// keybindings.json gives it something other than its built-in binding.
// Settings › Keybindings is the only reader of Overridden -- it is what
// lights up a row's Reset and the section's "Reset every shortcut" -- but it
// rides on every hello and every keyTableMsg alike, the same as every other
// field here, rather than being asked for on its own.
type keyView struct {
	help.Key
	Overridden bool `json:"overridden,omitempty"`
}

// sendHello gives a freshly connected window the key table and the prefs. It
// runs on the workspace goroutine, which is what owns s.prefs.
func (s *Server) sendHello(c *controlClient) {
	msg := helloMsg{Type: "hello", Keys: effectiveKeys(c.remote), Prefs: s.prefs, Remote: c.remote}
	if c.remote {
		if ra := s.remoteAccess(); ra != nil {
			msg.E2EPublicKey = ra.E2EPublicKey()
		}
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.send(data)
}

// effectiveKeys is the key table a window is sent: help.Keys with every
// remapping in keybindings.json applied, and -- for a window reached through
// the relay -- without what only the desk may do.
//
// The overrides are read fresh each call, the way agents.json is: a remap
// saved from one window has to reach every other window's palette and
// keydown handling, and it does, through keyTableMsg, but a window connecting
// for the first time has no earlier keyTableMsg to have caught.
func effectiveKeys(remote bool) []keyView {
	f, err := keybindings.Load()
	var overrides map[string]string
	if err == nil {
		overrides = f.Overrides
	}
	eff := keybindings.Effective(overrides)
	views := make([]keyView, len(eff))
	for i, k := range eff {
		_, overridden := overrides[k.ID]
		views[i] = keyView{Key: k, Overridden: overridden}
	}
	if !remote {
		return views
	}
	return remoteFiltered(views)
}

// remoteFiltered is keys without what a window reached through the relay
// cannot do. Detach and Quit are the desk's to choose -- a phone's window
// closing never stopped anything, and nothing on the phone could start the
// agents again -- and the server refuses both from there. Offered anyway, Quit
// asked whether to stop every agent only for the answer to be no.
func remoteFiltered(keys []keyView) []keyView {
	out := make([]keyView, 0, len(keys))
	for _, k := range keys {
		switch k.ID {
		case "detach", "quit":
			continue
		}
		out = append(out, k)
	}
	return out
}

// keyTableMsg tells every window the key table has changed -- a binding was
// remapped or reset -- so its palette, its tooltips and its keydown handling
// all pick up the new binding without a reconnect. It carries the whole
// table, the way prefsMsg carries the whole of the preferences, so a window
// need not reconcile a partial change against what it already has.
//
// Its type is "keyTable" rather than "keys", which the API keys dialog's
// message already is.
type keyTableMsg struct {
	Type string    `json:"type"`
	Keys []keyView `json:"keys"`
}

// broadcastKeys tells every window what the key table now is, one copy per
// window since a window reached through the relay is sent fewer actions.
func (s *Server) broadcastKeys() {
	for _, cl := range s.clientList() {
		data, err := json.Marshal(keyTableMsg{Type: "keyTable", Keys: effectiveKeys(cl.remote)})
		if err != nil {
			return
		}
		cl.send(data)
	}
}

// prefsMsg tells every window that the preferences changed, so a second window
// does not go on offering a hint that was dismissed in the first.
type prefsMsg struct {
	Type  string      `json:"type"`
	Prefs store.Prefs `json:"prefs"`
}

// updatePrefs applies one change, asked for by window c, to the preferences
// and, where it changed anything, saves them and tells every window.
//
// The change is made to what is on disk, not to the copy read at start-up.
// Written back whole, that copy undid whatever else had changed the file
// since: with two instances running (-solo) whichever saved last put back the
// other's old settings, and a start-up read that failed left defaults in
// memory which the first save then wrote over every setting there was.
//
// For the same reason nothing is saved when what is on disk could not be read
// just now. That read gives the defaults, and the defaults with one change
// saved over the file stood in for every setting the user had: the file itself
// was moved aside and kept, but the window came back on the defaults at the
// next start, with nothing to say where the rest had gone.
func (s *Server) updatePrefs(c *controlClient, change func(*store.Prefs) bool) {
	s.do(func() {
		p, err := store.ReadPrefs()
		if err != nil {
			// Only the window that made the change is told. Every other window
			// has nothing to do about it, and a phone reached through the relay
			// would be shown an error for a hint dismissed on the desk.
			c.notify("could not save the setting, because the settings saved before could not be read and saving now would write over them; it will not be kept after flockdeck restarts: "+err.Error(), true)
			return
		}
		if !change(&p) {
			return
		}
		if err := store.SavePrefs(p); err != nil {
			// Preferences are a convenience; failing to record one must not
			// interrupt what the person was actually doing. But the window has
			// already applied the change and said so, and it would go back at
			// the next start with nothing to say why -- so that is said now, to
			// the window that made it.
			c.notify("could not save the setting, so it will not be kept after flockdeck restarts: "+err.Error(), true)
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
func (s *Server) markHelpSeen(c *controlClient) {
	s.updatePrefs(c, func(p *store.Prefs) bool {
		if p.HelpSeen {
			return false
		}
		p.HelpSeen = true
		return true
	})
}

// dismissTip records that an inline hint has been sent away for good.
func (s *Server) dismissTip(c *controlClient, id string) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return p.Dismiss(id) })
}

// setFontSize records the size the terminals are drawn at. The window used to
// keep it itself, in local storage, which forgot it on every run.
func (s *Server) setFontSize(c *controlClient, size int) {
	if size < 8 || size > 28 {
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.FontSize, size) })
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
func (s *Server) setNotifications(c *controlClient, off bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.NotificationsOff, off) })
}

// setScrollback records how many lines each terminal keeps, within what a
// window will accept.
func (s *Server) setScrollback(c *controlClient, lines int) {
	if lines < 1000 || lines > 200000 {
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.Scrollback, lines) })
}

// setUpdates records whether the application checks for new releases, which
// could otherwise only be turned off with an environment variable.
func (s *Server) setUpdates(c *controlClient, off bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.UpdatesOff, off) })
}

// checkForUpdates answers the interface's manual "Check for updates" action.
// This package knows nothing of GitHub or releases -- that is OnCheckForUpdates,
// wired up by whatever started the server -- so all that is done here is
// running it and telling the window that asked what it found, since clicking
// the button is what asking to be told looks like.
func (s *Server) checkForUpdates(c *controlClient) {
	if s.OnCheckForUpdates == nil {
		c.notify("checking for updates is not available in this build", true)
		return
	}
	message, isErr := s.OnCheckForUpdates()
	c.notify(message, isErr)
}

// resetTips brings back every inline hint that was dismissed, which could
// otherwise only be done by editing prefs.json.
func (s *Server) resetTips(c *controlClient) {
	s.updatePrefs(c, func(p *store.Prefs) bool {
		if len(p.DismissedTips) == 0 {
			return false
		}
		p.DismissedTips = nil
		return true
	})
}

// setCursorSteady records whether the terminal cursors blink, which was fixed
// in the source.
func (s *Server) setCursorSteady(c *controlClient, steady bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.CursorSteady, steady) })
}

// setScreenReader records whether the terminals keep a copy of their lines for
// a screen reader. Without it nothing an agent wrote could be read out at all.
func (s *Server) setScreenReader(c *controlClient, on bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.ScreenReader, on) })
}

// setCursorStyle records the shape of the terminal cursors. A block is the
// default and is kept as nothing, so a file written before there was a choice
// reads the same; a shape the terminals do not draw is refused.
func (s *Server) setCursorStyle(c *controlClient, style string) {
	switch style {
	case "block":
		style = ""
	case "", "bar", "underline":
	default:
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.CursorStyle, style) })
}

// setFontFamily records the typeface the terminals are drawn in; empty goes
// back to the default. It arrives from a text field, so anything longer than
// a font list could sensibly be is refused rather than kept.
func (s *Server) setFontFamily(c *controlClient, family string) {
	family = strings.TrimSpace(family)
	if len(family) > 200 {
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.FontFamily, family) })
}

// railWidthMin and railWidthMax bound how wide the rail may be dragged or
// typed to: narrower and the names in it are not worth showing, wider and it
// is most of the window on anything but a very large one.
const (
	railWidthMin = 180
	railWidthMax = 480
)

// setRailExpanded records whether the rail is a panel of icons and names, or
// icons alone. The window used to keep this itself, in local storage, which
// forgot it on every run, like the font size above.
func (s *Server) setRailExpanded(c *controlClient, on bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.RailExpanded, on) })
}

// setRailWidth records how wide the rail is drawn while it is expanded,
// within what a window will still show something in.
func (s *Server) setRailWidth(c *controlClient, width int) {
	if width < railWidthMin || width > railWidthMax {
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.RailWidth, width) })
}

// setTheme records which palette the window is drawn in; a theme the window
// does not draw is refused rather than kept.
func (s *Server) setTheme(c *controlClient, theme string) {
	switch theme {
	case "dark":
		theme = ""
	case "", "light", "system":
	default:
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.Theme, theme) })
}

// setAccentColor records which of the fixed swatches the window's accent is;
// a colour outside that set would go illegible against one palette or the
// other and is refused. "" is the default blue.
func (s *Server) setAccentColor(c *controlClient, color string) {
	switch color {
	case "", "blue", "purple", "green", "orange", "pink", "teal":
	default:
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.AccentColor, color) })
}

// setFanOutSameTab records whether the fan-out dialog's "Put them in this
// tab" checkbox starts ticked.
func (s *Server) setFanOutSameTab(c *controlClient, on bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.FanOut.SameTab, on) })
}

// setAutoReviewDefault records the value Pane.AutoReview starts at for a pane
// with no parent. See Prefs.AutoReviewDefault.
func (s *Server) setAutoReviewDefault(c *controlClient, on bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.AutoReviewDefault, on) })
}

// setJevStatus records whether the last lines of a pane's output may be sent
// to TypeSafe's Jev to help read the status of panes that report none. See
// Prefs.JevStatus: the default is off, and only an explicit "on" turns it on.
func (s *Server) setJevStatus(c *controlClient, on bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.JevStatus, on) })
}

// setStatusLine records when a Claude pane's status line is routed through
// Flockdeck. It takes effect for a pane when it next starts, which is when
// its settings are written; a mode the panes do not know is refused.
func (s *Server) setStatusLine(c *controlClient, mode string) {
	switch mode {
	case "auto":
		mode = ""
	case "", "on", "off":
	default:
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.Spend.StatusLine, mode) })
}

// helpBody is what /help.json answers with, encoded the first time it is
// asked for. The pages are compiled in and rendered once already (help.Pages);
// encoding them afresh for every request cost half a millisecond and half a
// megabyte each time, for the same bytes.
var helpBody = sync.OnceValues(func() ([]byte, error) {
	pages, err := help.Pages()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"pages": pages})
})

// handleHelp serves the rendered help pages to an authorised window.
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	data, err := helpBody()
	if err != nil {
		http.Error(w, "the help pages could not be rendered", http.StatusInternalServerError)
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
