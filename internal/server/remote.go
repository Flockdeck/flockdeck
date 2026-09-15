package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
)

// Remote access puts this same server in front of a browser on another
// device. The relay carries each of that browser's connections to this process
// over a tunnel the desktop dialled out on, and they are served here by the
// very handlers the local window uses — so a remote window is not a second,
// lesser interface to keep level with the first, it is the first.
//
// What differs is how a request proves it may be answered. Locally that is the
// per-run token, which the window has from its URL. A remote browser never has
// it and never should: it is the key to everything on this loopback port, and
// the relay would be one more place it could be read out of. Instead a request
// is authorised by having arrived through the tunnel at all — the relay has
// already checked that the device asking is paired with this machine's account,
// and nothing else can put a connection on the tunnel.
//
// The endpoints only another launch of the binary uses — /health, /open, /quit
// and /remote/reload — insist on the token in the URL regardless, so none of
// them can be reached remotely. The hook and spawn endpoints are not on this
// server at all; they have a listener of their own on loopback.

// RemoteAccess is what the server needs of remote access: to say how the
// tunnel is, to reach the relay on the window's behalf, to be told to reread
// the enrolment, and to turn it on and off, rename this machine and try the
// relay again from the dialog as `flockdeck remote` does from a terminal, and
// to move this machine to another relay.
type RemoteAccess interface {
	Status() (remote.Status, bool)
	Client() (*remote.Client, error)
	Reload() error
	Enable(ctx context.Context, req remote.EnableRequest) (replaced bool, err error)
	Disable(ctx context.Context, force bool) (untold error, err error)
	Move(ctx context.Context, req remote.EnableRequest) (untold error, err error)
	Rename(ctx context.Context, name string) error
	Reconnect() error
}

type remoteHolder struct{ RemoteAccess }

// SetRemote hands the server its remote access, which may be nil.
func (s *Server) SetRemote(r RemoteAccess) {
	if r == nil {
		s.remote.Store(nil)
		return
	}
	s.remote.Store(&remoteHolder{r})
}

func (s *Server) remoteAccess() RemoteAccess {
	if h := s.remote.Load(); h != nil {
		return h.RemoteAccess
	}
	return nil
}

// remoteKey marks a request as having come through the tunnel.
type remoteKey struct{}

// fromRemote reports whether a request came through the tunnel. Only
// RemoteHandler sets the mark, and it is only ever served on the tunnel's
// listener, so there is no way to claim it from loopback.
func fromRemote(r *http.Request) bool {
	v, _ := r.Context().Value(remoteKey{}).(bool)
	return v
}

// RemoteHandler is the window's own set of handlers, with every request marked
// as having arrived through the tunnel.
func (s *Server) RemoteHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), remoteKey{}, true)))
	})
}

// ServeRemote answers the connections arriving on l, which is one tunnel's
// worth of them, until l is closed or the server is.
//
// Closing l ends the serving but not a WebSocket already handed over: those
// have left net/http's hands, and it is closing the tunnel under them — which
// is what the caller does when it closes l — that ends them.
func (s *Server) ServeRemote(l net.Listener) error {
	hs := &http.Server{Handler: s.RemoteHandler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-s.closed:
			_ = hs.Close()
		case <-done:
		}
	}()
	err := hs.Serve(l)
	_ = hs.Close()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// LocalClientCount reports how many windows on this machine are connected —
// every window, that is, but the ones reached through the relay.
//
// It is what decides whether the application is still being looked at. A
// remote window is somebody elsewhere, and whether it is open says nothing
// about the window on the desk: closing that one still quits an instance that
// was not detached, and a remote window closing never quits anything. The way
// to leave the agents running for a remote window to find is the same as it
// always was — detach.
func (s *Server) LocalClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for c := range s.clients {
		if !c.remote {
			n++
		}
	}
	return n
}

// remoteClientCount is the other half: windows reached through the relay.
func (s *Server) remoteClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for c := range s.clients {
		if c.remote {
			n++
		}
	}
	return n
}

// handleRemoteReload tells the instance that the enrolment on disk has
// changed, which is how `flockdeck remote enable` and `disable` reach a running
// one. Like /open it acts rather than reports, so it wants a POST and the
// token in the URL.
func (s *Server) handleRemoteReload(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) || !s.requireURLToken(w, r) {
		return
	}
	ra := s.remoteAccess()
	if ra == nil {
		http.Error(w, "this instance has no remote access to reload", http.StatusServiceUnavailable)
		return
	}
	if err := ra.Reload(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RequestRemoteReload asks a running instance to reread the enrolment.
func RequestRemoteReload(baseURL, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/remote/reload?t="+token, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return refused("reload remote access", resp)
	}
	return nil
}

// remoteView is the tunnel as the window shows it.
type remoteView struct {
	State   string     `json:"state"`
	Detail  string     `json:"detail,omitempty"`
	Since   time.Time  `json:"since"`
	RetryAt *time.Time `json:"retryAt,omitempty"`
	Relay   string     `json:"relay"`
	HostID  string     `json:"hostId"`
	Name    string     `json:"name,omitempty"`
	// Viewers is how many windows are open on this instance through the
	// relay, which is worth knowing at the desk: somebody may be typing.
	Viewers int `json:"viewers"`
	// PushError is why the last push notification asked of the relay failed,
	// in its words, for Settings to say. See push.go.
	PushError string `json:"pushError,omitempty"`
}

// remoteSnapshot is the tunnel for the state message, or nil when this
// machine is not enrolled.
func (s *Server) remoteSnapshot() *remoteView {
	ra := s.remoteAccess()
	if ra == nil {
		return nil
	}
	st, ok := ra.Status()
	if !ok {
		return nil
	}
	v := &remoteView{
		State: string(st.State), Detail: st.Detail, Since: st.Since,
		Relay: st.Relay, HostID: st.HostID, Name: st.Name,
		Viewers:   s.remoteClientCount(),
		PushError: s.pushError(),
	}
	if !st.RetryAt.IsZero() {
		at := st.RetryAt
		v.RetryAt = &at
	}
	return v
}

// ---------------------------------------------------------------------------
// The remote access dialog
// ---------------------------------------------------------------------------

type remoteDevicesMsg struct {
	Type    string          `json:"type"`
	Enabled bool            `json:"enabled"`
	Devices []remote.Device `json:"devices"`
	Hosts   []remote.Host   `json:"hosts"`
	// Current is the device this window is on, when it is one — so the list
	// can say which entry revoking would end the session in front of you.
	Current string `json:"current,omitempty"`
	Error   string `json:"error,omitempty"`
	// Plan is the account's plan, when the relay reports one. The window
	// shows it, and nothing here acts on it.
	Plan *remote.Plan `json:"plan,omitempty"`
}

type remotePairMsg struct {
	Type      string    `json:"type"`
	Kind      string    `json:"kind"`
	Code      string    `json:"code,omitempty"`
	URL       string    `json:"url,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	// QR is the link drawn as a QR code, an SVG document.
	QR    string `json:"qr,omitempty"`
	Error string `json:"error,omitempty"`
}

// remoteDevices lists what the account has paired. It goes to the relay, so
// it is done off the connection's own goroutine.
func (s *Server) remoteDevices(c *controlClient) {
	go func() {
		defer s.surviveFor(c, "listing paired devices")
		msg := remoteDevicesMsg{
			Type: "remoteDevices", Devices: []remote.Device{}, Hosts: []remote.Host{},
			Current: c.device,
		}
		cl, err := s.remoteClient()
		switch {
		case errors.Is(err, remote.ErrNotEnabled):
			c.sendJSON(msg)
			return
		case err != nil:
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		msg.Enabled = true
		roster, err := cl.Devices(context.Background())
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		if roster.Devices != nil {
			msg.Devices = roster.Devices
		}
		if roster.Hosts != nil {
			msg.Hosts = roster.Hosts
		}
		msg.Plan = roster.Plan
		c.sendJSON(msg)
	}()
}

// remotePair asks the relay for a pairing code. A device code comes back with
// its link, and the link drawn as a QR code, which is how it usually reaches
// the device: photographed off this screen.
func (s *Server) remotePair(c *controlClient, kind string) {
	if kind != remote.KindHost {
		kind = remote.KindDevice
	}
	// A join code takes another desktop into this account, after which every
	// device paired with it reaches this machine and that one reaches every
	// agent here. Asked for from a window reached through the relay -- a
	// phone left unlocked, say -- it is a way back in that outlasts unpairing
	// the phone. The dialog there offers only a device code; this is for a
	// window that sends it anyway.
	if kind == remote.KindHost && c.remote {
		c.sendJSON(remotePairMsg{Type: "remotePair", Kind: kind, Error: deskOnlyJoin})
		c.notify(deskOnlyJoin, true)
		return
	}
	go func() {
		defer s.surviveFor(c, "pairing a device")
		msg := remotePairMsg{Type: "remotePair", Kind: kind}
		cl, err := s.remoteClient()
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		p, err := cl.Pair(context.Background(), kind)
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		msg.Code, msg.URL, msg.ExpiresAt = p.Code, p.URL, p.ExpiresAt
		if p.URL != "" {
			// A code that cannot be drawn is still a working link, so it is
			// sent without the picture rather than not at all.
			if svg, err := remote.QRSVG(p.URL); err == nil {
				msg.QR = svg
			}
		}
		c.sendJSON(msg)
	}()
}

// deskOnlyJoin is what a window reached through the relay is told when it asks
// for a code that takes another desktop into this account.
const deskOnlyJoin = "a code for another desktop to join this account is made on the machine flockdeck runs on, not from a window reached through the relay"

// remoteRevoke unpairs a device and sends the list again.
func (s *Server) remoteRevoke(c *controlClient, id string) {
	if id == "" {
		c.notify("no device was named", true)
		return
	}
	go func() {
		defer s.surviveFor(c, "unpairing a device")
		cl, err := s.remoteClient()
		if err != nil {
			c.notify(err.Error(), true)
			return
		}
		if err := cl.Revoke(context.Background(), id); err != nil {
			c.notify("could not unpair that device: "+err.Error(), true)
		} else {
			c.notify("device unpaired", false)
		}
		s.remoteDevices(c)
	}()
}

// remoteRename renames this machine, or one of the account's devices, from
// the dialog, and sends the list again with the new name in it. A name that
// could not be one is refused before the relay is asked.
func (s *Server) remoteRename(c *controlClient, kind, id, name string) {
	clean, err := remote.CheckName(name)
	if err != nil {
		c.notify("could not rename it: "+err.Error(), true)
		return
	}
	if kind != remote.KindHost && id == "" {
		c.notify("no device was named", true)
		return
	}
	go func() {
		defer s.surviveFor(c, "renaming")
		ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
		defer cancel()
		what := "this machine"
		var err error
		if kind == remote.KindHost {
			// Through remote access rather than the relay alone, so that the
			// name is saved here too and the window shows it.
			if ra := s.remoteAccess(); ra == nil {
				err = remote.ErrNotEnabled
			} else {
				err = ra.Rename(ctx, clean)
			}
		} else {
			what = "the device"
			var cl *remote.Client
			if cl, err = s.remoteClient(); err == nil {
				err = cl.RenameDevice(ctx, id, clean)
			}
		}
		if err != nil {
			c.notify("could not rename "+what+": "+err.Error(), true)
		} else {
			c.notify(what+" is now called “"+clean+"”", false)
		}
		s.remoteDevices(c)
	}()
}

// remoteOutcomeMsg answers a window that turned remote access on or off, or
// asked for the relay to be tried again. An error is shown under the button
// that asked, in the words remote access gave it, which name the field at
// fault rather than a flag.
type remoteOutcomeMsg struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
	// Untold is a disable that could not reach the relay. The window offers to
	// try again first, since that is most often a network down for now, and to
	// forget the enrolment here anyway second.
	Untold bool `json:"untold,omitempty"`
	// Warning is why the relay was not told, after it was forgotten anyway.
	Warning string `json:"warning,omitempty"`
}

// remoteCallTimeout bounds a relay call the dialog is waiting on. It is long,
// for a relay on the far side of a slow link, and still an answer.
const remoteCallTimeout = 30 * time.Second

// remoteEnable enrols this machine from the dialog: `flockdeck remote enable`
// without a terminal. Enrolling decides where the traffic goes, which is why
// the dialog shows the relay it will use before it is pressed.
func (s *Server) remoteEnable(c *controlClient, cmd command) {
	if refusedThroughRelay(c, "enable") {
		return
	}
	req := remote.EnableRequest{Relay: cmd.Relay, Name: cmd.Name, Join: cmd.Join, Invite: cmd.Invite}
	s.remoteCall(c, "enable", "turning remote access on", func(ctx context.Context, ra RemoteAccess, msg *remoteOutcomeMsg) {
		replaced, err := ra.Enable(ctx, req)
		switch {
		case err != nil:
			msg.Error = err.Error()
		case replaced:
			c.notify("remote access is on: the relay had forgotten this machine, so it was enrolled again", false)
		default:
			c.notify("remote access is on", false)
		}
	})
}

// remoteDisable takes this machine off its relay from the dialog. A relay that
// cannot be told is not taken for one that was: the window is asked whether to
// forget the enrolment regardless, which leaves the machine listed there.
func (s *Server) remoteDisable(c *controlClient, force bool) {
	if refusedThroughRelay(c, "disable") {
		return
	}
	s.remoteCall(c, "disable", "turning remote access off", func(ctx context.Context, ra RemoteAccess, msg *remoteOutcomeMsg) {
		untold, err := ra.Disable(ctx, force)
		var notTold *remote.RelayUntoldError
		switch {
		case errors.As(err, &notTold):
			msg.Untold, msg.Error = true, err.Error()
		case err != nil:
			msg.Error = err.Error()
		default:
			if untold != nil {
				msg.Warning = "The relay could not be told, so it will go on listing this machine, offline: " + untold.Error()
			}
			c.notify("remote access is off", false)
		}
	})
}

// deskOnlyRemote is why a window reached through the relay may not turn remote
// access off or on. Off cuts the way in that window came by, and nothing at
// the far end can turn it on again; on, from a window that is already in, can
// only be against another relay -- moving everything typed at the desk, and
// everything the agents print, to a relay chosen from somewhere else.
const deskOnlyRemote = "remote access is turned off, or moved to another relay, on the machine flockdeck runs on — not from a window reached through the relay"

// refusedThroughRelay refuses a window reached through the relay that asked to
// turn remote access on or off, and reports whether it did. The dialog's
// button waits for an outcome, so one is sent as well as the notice.
func refusedThroughRelay(c *controlClient, action string) bool {
	if !c.remote {
		return false
	}
	c.sendJSON(remoteOutcomeMsg{Type: "remoteOutcome", Action: action, Error: deskOnlyRemote})
	c.notify(deskOnlyRemote, true)
	return true
}

// remoteMove moves this machine to another relay from the dialog: `flockdeck
// remote move` without a terminal. The window has already said that every
// device will pair again, and asked, before this is sent.
func (s *Server) remoteMove(c *controlClient, cmd command) {
	req := remote.EnableRequest{Relay: cmd.Relay, Name: cmd.Name, Join: cmd.Join, Invite: cmd.Invite}
	s.remoteCall(c, "move", "moving to another relay", func(ctx context.Context, ra RemoteAccess, msg *remoteOutcomeMsg) {
		untold, err := ra.Move(ctx, req)
		switch {
		case err != nil:
			msg.Error = err.Error()
			return
		case untold != nil:
			msg.Warning = "The old relay could not be told, so it will go on listing this machine, offline, until a device paired there removes it: " + untold.Error()
		}
		c.notify("this machine has moved to the new relay; pair each device again", false)
	})
}

// remoteReconnect is the dialog's "try again": the relay is tried now rather
// than when the tunnel's own wait runs out.
func (s *Server) remoteReconnect(c *controlClient) {
	s.remoteCall(c, "reconnect", "trying the relay again", func(_ context.Context, ra RemoteAccess, msg *remoteOutcomeMsg) {
		if err := ra.Reconnect(); err != nil {
			msg.Error = err.Error()
		}
	})
}

// remoteCall runs one of the dialog's requests off the connection's own
// goroutine, since each goes to the relay, and answers it with what came of
// it and the roster again, which turning remote access on or off changes.
func (s *Server) remoteCall(c *controlClient, action, what string, call func(context.Context, RemoteAccess, *remoteOutcomeMsg)) {
	go func() {
		defer s.surviveFor(c, what)
		msg := remoteOutcomeMsg{Type: "remoteOutcome", Action: action}
		ra := s.remoteAccess()
		if ra == nil {
			msg.Error = "remote access is not available in this instance"
			c.sendJSON(msg)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
		defer cancel()
		call(ctx, ra, &msg)
		c.sendJSON(msg)
		s.remoteDevices(c)
	}()
}

func (s *Server) remoteClient() (*remote.Client, error) {
	ra := s.remoteAccess()
	if ra == nil {
		return nil, remote.ErrNotEnabled
	}
	return ra.Client()
}
