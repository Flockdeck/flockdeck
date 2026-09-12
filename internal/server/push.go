package server

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// Push notifications: when a pane has been waiting on its person for a while,
// the relay is asked to tell the account's paired devices. It does that by
// Web Push, to each device whose client was asked to with its Notify me
// button, whether or not the client is open there.
//
// A wait is noticed here, on a timer of its own, rather than with the state
// the windows are sent: that is not built at all while no window is open, and
// a run left detached for someone to answer from their phone is the one this
// is most for. Each wait is pushed once, when it has lasted the delay, and is
// known by when it began, Sess.StatusSince: a pane that stops waiting and then
// waits again is a new wait. The relay is asked off the workspace goroutine,
// as every other call to it is.
//
// Whether the account may be sent pushes is the relay's to decide, not this
// application's, which is free and gates nothing. A refusal is shown in
// Settings in the relay's own words.

// waitTick is how often the panes are looked at for a wait that has lasted.
// A wait is pushed at most this long after its delay has passed.
var waitTick = time.Second

// The delay before a wait is pushed, and the bounds of what it may be set to.
const (
	defaultPushDelay = 30 * time.Second
	minPushDelay     = 5 * time.Second
	maxPushDelay     = time.Hour
)

// pushState is the server's part in push notifications.
type pushState struct {
	// pushed is, for each pane whose wait has been pushed, when that wait
	// began. It is touched only on the workspace goroutine.
	pushed map[string]time.Time
	// send asks the relay for a push; nil is the relay itself. A test
	// replaces it, on the workspace goroutine, which is where it is read.
	send func(context.Context, remote.Notification) error
	// err is why the last push asked for failed, or empty, for Settings to
	// say.
	err atomic.Pointer[string]
}

// pushDelay is how long a wait lasts before it is pushed.
func pushDelay(p store.PushPrefs) time.Duration {
	d := time.Duration(p.DelaySeconds) * time.Second
	if d < minPushDelay || d > maxPushDelay {
		return defaultPushDelay
	}
	return d
}

// waitLoop looks for waits to push, for as long as the server runs.
func (s *Server) waitLoop() {
	tick := time.NewTicker(waitTick)
	defer tick.Stop()
	for {
		select {
		case <-s.closed:
			return
		case now := <-tick.C:
			// A machine that is not enrolled has nobody to tell, and the
			// workspace goroutine is not bothered to find that out.
			if s.remoteAccess() == nil {
				continue
			}
			s.do(func() { s.watchWaits(now) })
		}
	}
}

// watchWaits asks the relay to push every wait that is due. It runs on the
// workspace goroutine, and the asking is done off it.
func (s *Server) watchWaits(now time.Time) {
	due := s.pushesDue(now)
	if len(due) == 0 {
		return
	}
	send := s.push.send
	if send == nil {
		send = s.notifyRelay
	}
	for _, n := range due {
		go func() {
			defer s.survive("sending a push notification")
			ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
			defer cancel()
			s.pushOutcome(send(ctx, n))
		}()
	}
}

// notifyRelay asks the relay this machine is enrolled with for a push.
func (s *Server) notifyRelay(ctx context.Context, n remote.Notification) error {
	cl, err := s.remoteClient()
	if err != nil {
		return err
	}
	_, err = cl.Notify(ctx, n)
	return err
}

// pushOutcome records how the last push went, and has the windows told when
// that changed what Settings says.
func (s *Server) pushOutcome(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if old := s.push.err.Swap(&msg); (old == nil && msg == "") || (old != nil && *old == msg) {
		return
	}
	s.Wake()
}

// pushError is why the last push failed, or empty.
func (s *Server) pushError() string {
	if p := s.push.err.Load(); p != nil {
		return *p
	}
	return ""
}

// pushesDue is the pushes to ask for now: one for each pane that has been
// waiting for the delay and whose wait has not been pushed. It runs on the
// workspace goroutine.
func (s *Server) pushesDue(now time.Time) []remote.Notification {
	ra := s.remoteAccess()
	if ra == nil || s.prefs.Push.Off {
		return nil
	}
	st, ok := ra.Status()
	if !ok {
		return nil
	}
	delay := pushDelay(s.prefs.Push)
	var due []remote.Notification
	var names map[string]string
	waiting := map[string]bool{}
	for _, t := range s.ws.Tabs {
		for _, id := range t.Tree.Panes() {
			p := s.ws.Pane(id)
			if p == nil || p.Sess == nil {
				continue
			}
			if status, _ := p.Status(); status != session.StatusWaiting {
				continue
			}
			waiting[id] = true
			since := p.Sess.StatusSince()
			if now.Sub(since) < delay || s.push.pushed[id].Equal(since) {
				continue
			}
			if s.push.pushed == nil {
				s.push.pushed = map[string]time.Time{}
			}
			s.push.pushed[id] = since
			if names == nil {
				names = map[string]string{}
				for _, pr := range s.ws.Projects() {
					names[pr.Root] = pr.Name
				}
			}
			due = append(due, pushFor(p.ID, p.Name, projectLabel(names, s.ws.RootOf(p.ID)), st.Name, s.prefs.Push.Anonymous))
		}
	}
	// A pane no longer waiting, or gone, has its next wait pushed afresh.
	for id := range s.push.pushed {
		if !waiting[id] {
			delete(s.push.pushed, id)
		}
	}
	return due
}

// pushFor is what a push about one pane says: its name and project, and the
// machine it is on -- or, sent anonymously, only that an agent on the machine
// needs you. The pane's id goes either way, since it is what a tap opens, and
// names nothing.
func pushFor(paneID, pane, project, desktop string, anonymous bool) remote.Notification {
	if desktop == "" {
		desktop = "your desktop"
	}
	if anonymous {
		return remote.Notification{PaneID: paneID, Title: "An agent on " + desktop + " needs you"}
	}
	title := pane
	if title == "" {
		title = "An agent"
	}
	if project != "" {
		title += " · " + project
	}
	return remote.Notification{PaneID: paneID, Title: title + " needs you", Body: "On " + desktop}
}

// setPushOff records whether the paired devices are notified.
func (s *Server) setPushOff(off bool) {
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.Push.Off, off) })
}

// setPushAnonymous records whether a push names the pane and its project.
func (s *Server) setPushAnonymous(on bool) {
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.Push.Anonymous, on) })
}

// setPushDelay records how long a wait lasts before it is pushed, within the
// bounds. The default is kept as nothing.
func (s *Server) setPushDelay(seconds int) {
	d := time.Duration(seconds) * time.Second
	if d < minPushDelay || d > maxPushDelay {
		return
	}
	if d == defaultPushDelay {
		seconds = 0
	}
	s.updatePrefs(func(p *store.Prefs) bool { return setPref(&p.Push.DelaySeconds, seconds) })
}
