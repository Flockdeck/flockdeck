package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	osidle "github.com/jmwri/flockdeck/internal/idle"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// Push notifications: when an agent has been waiting on its person for a
// while, the relay is asked to tell the account's paired devices. It does that
// by Web Push, to each device whose client was asked to with its Notify me
// button, whether or not the client is open there. What a push says is sealed
// here for each device, and the relay only posts it: see remote.Push.
//
// A wait is noticed here, on a timer of its own, rather than with the state
// the windows are sent: that is not built at all while no window is open, and
// a run left detached for someone to answer from their phone is the one this
// is most for. A wait is known by when it began, Sess.StatusSince: a pane that
// stops waiting and then waits again is a new wait.
//
// The phone is told once for however many agents are waiting. One push says
// how many, and which, and replaces the one before it on the phone; pushes
// come a minute apart at most. Six agents fanned out from one plan reach the
// same question within a second of each other, and a phone that buzzes six
// times is a phone put on silent. The relay is asked off the workspace
// goroutine, as every other call to it is.
//
// Whether the account may be sent pushes is the relay's to decide, not this
// application's, which is free and gates nothing. A refusal is shown in
// Settings in the relay's own words.

// waitTick is how often the panes are looked at for a wait that has lasted.
// A wait is pushed at most this long after its delay has passed.
var waitTick = time.Second

// minPushGap is the least time between two pushes from this machine. A wait
// that comes due sooner is told of when it has passed, in the push that
// counts every agent waiting then.
var minPushGap = time.Minute

// phoneLook is how lately a pane has to have been used from a phone for its
// wait not to be pushed to the phone: see relayUse.
const phoneLook = 2 * time.Minute

// The delay before a wait is pushed, and the bounds of what it may be set to.
const (
	defaultPushDelay = 30 * time.Second
	minPushDelay     = 5 * time.Second
	maxPushDelay     = time.Hour
)

// pushState is the server's part in push notifications.
type pushState struct {
	// pushed is, for each waiting pane the devices have been told of, when
	// its wait began. It and last are touched only on the workspace
	// goroutine.
	pushed map[string]time.Time
	// last is when the last push was asked for.
	last time.Time
	// send asks the relay for a push; nil is the relay itself. A test
	// replaces it, on the workspace goroutine, which is where it is read.
	send func(context.Context, remote.Notification) error
	// err is why the last push asked for failed, or empty, for Settings to
	// say.
	err atomic.Pointer[string]

	// idleSince asks the OS how long it has gone without keyboard or mouse
	// input, anywhere, and whether it is locked; nil is idle.Since. A test
	// replaces it, on the workspace goroutine, which is where it is read.
	idleSince func() (d time.Duration, locked bool, ok bool)

	// idleAsk is when the OS next needs asking, and idleNone whether it
	// answered nothing when last asked; see deskInUse. Asking on Linux and
	// macOS runs a command, and an agent can wait on somebody at the desk
	// for hours, so an answer is kept until it could have changed.
	idleMu   sync.Mutex
	idleAsk  time.Time
	idleNone bool

	// desk is, for each window on this machine, when it last reported real
	// keyboard or mouse input; see setDeskUsed. It backs deskInUse only when
	// idleSince cannot say -- the OS asked has no answer for this machine.
	deskMu sync.Mutex
	desk   map[*controlClient]time.Time
}

// deskLook is how lately this computer has to have seen keyboard or mouse
// input, in any application, for the phone not to be told of a wait: see
// deskInUse.
const deskLook = 2 * time.Minute

// setDeskUsed records that a window on this machine just reported real
// input -- a keystroke, a click, a scroll, the pointer moving -- as opposed
// to merely coming to the front. It backs deskInUse for the platforms idle
// cannot read; a window reached through the relay is not this desk, and one
// that has gone reports nothing more.
func (s *Server) setDeskUsed(c *controlClient, used bool) {
	if c == nil || c.remote {
		return
	}
	s.push.deskMu.Lock()
	defer s.push.deskMu.Unlock()
	if !used {
		delete(s.push.desk, c)
		return
	}
	if s.push.desk == nil {
		s.push.desk = map[*controlClient]time.Time{}
	}
	s.push.desk[c] = time.Now()
}

// deskWindowUsed is deskInUse's fallback for when the OS cannot say how idle
// this machine is: whether a Flockdeck window here has reported real input
// within deskLook. Coming to the front does not, by itself, count as that.
func (s *Server) deskWindowUsed(now time.Time) bool {
	s.push.deskMu.Lock()
	defer s.push.deskMu.Unlock()
	for _, at := range s.push.desk {
		if now.Sub(at) < deskLook {
			return true
		}
	}
	return false
}

// deskInUse reports whether somebody is at this desk: this computer has seen
// keyboard or mouse input, in any application, within deskLook of now -- or,
// where the OS cannot be asked, a Flockdeck window here has. Whether any
// window is in front makes no difference either way. A locked screen is
// always away, whatever the idle time.
//
// An idle time of d, under deskLook, cannot reach deskLook sooner than
// deskLook-d from now, so the OS is not asked again before then; a screen
// locked in the meantime is noticed at the latest when the idle time would
// have been. An OS with no answer is asked again after a minute.
func (s *Server) deskInUse(now time.Time) bool {
	s.push.idleMu.Lock()
	defer s.push.idleMu.Unlock()
	if now.Before(s.push.idleAsk) {
		if s.push.idleNone {
			return s.deskWindowUsed(now)
		}
		return true
	}
	since := s.push.idleSince
	if since == nil {
		since = osidle.Since
	}
	d, locked, ok := since()
	s.push.idleNone = !ok
	switch {
	case !ok:
		s.push.idleAsk = now.Add(time.Minute)
		return s.deskWindowUsed(now)
	case locked || d >= deskLook:
		s.push.idleAsk = time.Time{}
		return false
	default:
		s.push.idleAsk = now.Add(deskLook - d)
		return true
	}
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

// watchWaits asks the relay for the push that is due, if one is. It runs on
// the workspace goroutine, and the asking is done off it.
func (s *Server) watchWaits(now time.Time) {
	n := s.pushDue(now)
	if n == nil {
		return
	}
	send := s.push.send
	if send == nil {
		send = s.notifyRelay
	}
	go func() {
		defer s.survive("sending a push notification")
		ctx, cancel := context.WithTimeout(context.Background(), remoteCallTimeout)
		defer cancel()
		s.pushOutcome(send(ctx, *n))
	}()
}

// notifyRelay has the relay this machine is enrolled with push to the
// account's devices.
func (s *Server) notifyRelay(ctx context.Context, n remote.Notification) error {
	cl, err := s.remoteClient()
	if err != nil {
		return err
	}
	_, err = cl.Push(ctx, n)
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

// waitingAgent is a pane waiting on its person, as a push tells of it.
type waitingAgent struct {
	id, name, project string
	since             time.Time
}

// pushDue is the push to ask for now, or nil. One is due when an agent has
// been waiting for the delay and the devices have not been told of that wait,
// and a minute has passed since the last push. It tells of every agent
// waiting, those whose own delay has not passed yet included: they are
// counted in it, and not told of again one by one. It runs on the workspace
// goroutine.
func (s *Server) pushDue(now time.Time) *remote.Notification {
	ra := s.remoteAccess()
	if ra == nil || s.prefs.Push.Off {
		return nil
	}
	st, ok := ra.Status()
	if !ok {
		return nil
	}
	delay := pushDelay(s.prefs.Push)
	var waits []waitingAgent
	var names map[string]string
	waitingNow := map[string]bool{}
	fresh := false
	for _, t := range s.ws.Tabs {
		for _, id := range t.Tree.Panes() {
			p := s.ws.Pane(id)
			if p == nil || p.Sess == nil {
				continue
			}
			if status, _ := p.Status(); status != session.StatusWaiting {
				continue
			}
			waitingNow[id] = true
			// Somebody using this pane from their phone has seen it waiting,
			// and is not told of it there as well. It is told of if it is
			// still waiting once they have left it a while.
			if relayUse.since(id, now.Add(-phoneLook)) {
				continue
			}
			if names == nil {
				names = map[string]string{}
				for _, pr := range s.ws.Projects() {
					names[pr.Root] = pr.Name
				}
			}
			w := waitingAgent{id: id, name: p.Name, project: projectLabel(names, s.ws.RootOf(id)), since: p.Sess.StatusSince()}
			waits = append(waits, w)
			if now.Sub(w.since) >= delay && !s.push.pushed[id].Equal(w.since) {
				fresh = true
			}
		}
	}
	// A pane no longer waiting, or gone, has its next wait told afresh.
	for id := range s.push.pushed {
		if !waitingNow[id] {
			delete(s.push.pushed, id)
		}
	}
	if !fresh || now.Sub(s.push.last) < minPushGap {
		return nil
	}
	// Somebody at this desk can see who is waiting. The OS is asked how idle
	// it is only now, since a push is otherwise due -- not on every tick of
	// the wait loop, which would ask it once a second whether or not anyone
	// is waiting at all.
	if s.deskInUse(now) {
		return nil
	}
	if s.push.pushed == nil {
		s.push.pushed = map[string]time.Time{}
	}
	for _, w := range waits {
		s.push.pushed[w.id] = w.since
	}
	s.push.last = now
	n := pushAbout(st.HostID, st.Name, waits, s.prefs.Push.Anonymous)
	return &n
}

// pushAbout is what a push says of the agents waiting on this machine: the
// one, by its name and project, and its pane, which a tap opens; or how many,
// by name, and the machine's list of panes. Sent anonymously it says only how
// many, and on which machine. The ids in its page name nothing. Its tag is the
// machine's, so each push replaces the one before it on the phone.
func pushAbout(hostID, desktop string, waits []waitingAgent, anonymous bool) remote.Notification {
	if desktop == "" {
		desktop = "your desktop"
	}
	n := remote.Notification{URL: "/d/" + url.PathEscape(hostID), Tag: "flockdeck-" + hostID}
	if len(waits) == 1 {
		w := waits[0]
		n.URL += "/" + url.PathEscape(w.id)
		if anonymous {
			n.Title = "An agent on " + desktop + " needs you"
			return n
		}
		title := w.name
		if title == "" {
			title = "An agent"
		}
		if w.project != "" {
			title += " · " + w.project
		}
		n.Title, n.Body = title+" needs you", "On "+desktop
		return n
	}
	if anonymous {
		n.Title = fmt.Sprintf("%d agents on %s need you", len(waits), desktop)
		return n
	}
	names := make([]string, 0, len(waits))
	for _, w := range waits {
		name := w.name
		if name == "" {
			name = "an agent"
		}
		names = append(names, name)
	}
	n.Title = fmt.Sprintf("%d agents need you", len(waits))
	n.Body = "On " + desktop + ": " + strings.Join(names, ", ")
	return n
}

// setPushOff records whether the paired devices are notified.
func (s *Server) setPushOff(c *controlClient, off bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.Push.Off, off) })
}

// setPushAnonymous records whether a push names the pane and its project.
func (s *Server) setPushAnonymous(c *controlClient, on bool) {
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.Push.Anonymous, on) })
}

// setPushDelay records how long a wait lasts before it is pushed, within the
// bounds. The default is kept as nothing.
func (s *Server) setPushDelay(c *controlClient, seconds int) {
	d := time.Duration(seconds) * time.Second
	if d < minPushDelay || d > maxPushDelay {
		return
	}
	if d == defaultPushDelay {
		seconds = 0
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return setPref(&p.Push.DelaySeconds, seconds) })
}
