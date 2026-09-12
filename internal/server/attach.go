package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// healthMsg is what a second launch reads to decide whether the recorded
// instance is really alive.
type healthMsg struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
	Projects int    `json:"projects"`
	// Ready is false when the port is answering but the workspace behind it
	// has not come back yet. The instance is there either way, which is the
	// distinction that matters: a launch that cannot tell the two apart
	// writes off a running instance and starts a rival set of agents.
	Ready bool `json:"ready"`
	// Windows is how many windows on this machine are open onto the instance,
	// leaving out the ones reached through the relay. A launch that finds one
	// open has a window to bring back rather than a reason to open another.
	Windows int `json:"windows"`
}

// Version is reported by the health endpoint. main sets it at startup.
var Version = "dev"

// probeTimeout is how long a second launch waits for an answer to /health.
// healthTimeout, the wait for the workspace goroutine behind it, is
// deliberately shorter: an instance whose port answers but whose workspace has
// wedged should say so, rather than let the probe give up on its own side with
// nothing to report but a deadline.
//
// busyGrace is how long a probe keeps asking an instance that answers but says
// it is not ready. Being busy for a moment is ordinary -- opening a project
// and starting the agents in it happens on the workspace goroutine -- and
// giving up on that costs far more than waiting: the launch clears the
// instance record and starts a second set of agents alongside the first.
const (
	probeTimeout  = 2 * time.Second
	healthTimeout = probeTimeout / 2
)

// The two graces are variables so a test does not have to wait either of them
// out in full to see the giving up they end in.
var (
	busyGrace = 5 * time.Second
	busyRetry = 250 * time.Millisecond

	// quitGrace is how long RequestQuit waits for an instance it has asked to
	// stop to actually stop, and quitPoll how often it looks.
	quitGrace = 15 * time.Second
	quitPoll  = 100 * time.Millisecond

	// openTimeout is how long handleOpen gives the workspace to take a project
	// and say how it went. It is under the launch's own patience, so the
	// launch always hears an answer rather than giving up on its own side.
	openTimeout = 10 * time.Second
)

// handleHealth answers a probe from another launch of the binary.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.requireURLToken(w, r) {
		return
	}
	// One budget covers handing the question over and getting the answer
	// back. Handing it over is posted here rather than through s.do because
	// that waits without a deadline, and the queue in front of the workspace
	// is exactly what fills up while the workspace is slow -- so the one
	// endpoint whose whole job is to answer within a moment was the one that
	// could not answer at all.
	deadline := time.After(healthTimeout)
	done := make(chan int, 1)
	select {
	case s.cmds <- func() { done <- len(s.ws.Projects()) }:
	case <-s.closed:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	case <-deadline:
		s.notReady(w)
		return
	}

	var projects int
	select {
	case projects = <-done:
	case <-s.closed:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	case <-deadline:
		s.notReady(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthMsg{
		App: "flockdeck", Version: Version, PID: pid(), Projects: projects, Ready: true,
		Windows: s.LocalClientCount(),
	})
}

// notReady answers a probe the workspace could not be reached for.
//
// The port is still answering but the workspace behind it is not. Reporting a
// made-up project count would tell the second launch to hand its directory to
// an instance that cannot open it, and the window it expected would never
// appear. So this fails -- but it still says who it is, so the launch can wait
// for the workspace to come back rather than write the instance off and start
// a rival set of agents.
func (s *Server) notReady(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(healthMsg{
		App: "flockdeck", Version: Version, PID: pid(), Windows: s.LocalClientCount(),
	})
}

// handleOpen lets a second launch hand its directory to the running instance,
// so `flockdeck -C somewhere` attaches and opens that project rather than
// starting a rival server.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) || !s.requireURLToken(w, r) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "no path", http.StatusBadRequest)
		return
	}
	// One budget covers handing the work over and hearing how it went. Waiting
	// to hand it over has no deadline of its own, and the queue in front of
	// the workspace fills exactly when the workspace is slow, so counting only
	// the second half let this run past the launch's own patience and answer
	// nobody.
	deadline := time.After(openTimeout)
	errc := make(chan error, 1)
	select {
	case s.cmds <- func() {
		err := s.ws.OpenProject(path)
		if err == nil {
			s.wakeAsked()
		}
		errc <- err
	}:
	case <-s.closed:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	case <-deadline:
		// Nothing has been done at all -- the request never reached the
		// workspace -- so say that rather than leave the launch to guess
		// whether its project is about to open.
		http.Error(w, "the instance is busy", http.StatusServiceUnavailable)
		return
	}

	select {
	case err := <-errc:
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case <-s.closed:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	case <-deadline:
		// Taken, and still going. Reporting a failure for it was the wrong
		// answer twice over: the project does open, a moment later, and the
		// launch would meanwhile have refused to show a window onto it. The
		// only thing lost by not waiting for the verdict is an error about the
		// directory, and the launch has already checked that the directory is
		// there before asking.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireURLToken insists on the token in the request itself, rather than
// accepting the cookie the page holds.
//
// These three endpoints are for another launch of the binary: they report on
// the instance, hand it a project, or shut it down. The window's own page
// never calls any of them, and it matters that nothing else can. A cookie is
// no evidence of who is asking: cookies are scoped to a host and take no
// notice of the port, and a different port is not a different site, so
// anything else served from 127.0.0.1 or localhost -- the user's own dev
// server, or anything that can be made to serve a page from one -- had this
// instance's token attached to any request it chose to make here, with
// SameSite doing nothing to hold it back. A plain cross-origin POST needs no
// permission to be sent, and that its reply cannot be read is no comfort when
// the request itself stops every agent the user is running.
//
// The token in the URL is evidence, because a page from somewhere else does
// not have it and cannot read it: not from this server's replies, which are
// another origin to it, and not from the cookie, which is HttpOnly. The
// launching binary has it from the instance record and already sends it this
// way.
func (s *Server) requireURLToken(w http.ResponseWriter, r *http.Request) bool {
	if t := r.URL.Query().Get("t"); t != "" && s.tokenMatches(t) {
		return true
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return false
}

// requirePost rejects anything but the POST the launching binary sends.
//
// Both endpoints below act rather than report: one opens a project, the other
// shuts the application down. A GET carrying the token — a link followed by
// accident, a browser filling in an address from its history, a preview
// fetched on someone's behalf — should not be able to do either.
func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, "use POST", http.StatusMethodNotAllowed)
	return false
}

// handleQuit stops the running instance from outside, which is how
// `flockdeck -quit` reaches a detached one.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) || !s.requireURLToken(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
	// The answer has to be on the wire before the shutdown starts. Quitting
	// ends the process, and a reply still sitting in the connection's buffer
	// dies with it -- leaving `flockdeck -quit` to report a broken connection for
	// a shutdown that in fact worked, and a script around it to retry or fail.
	_ = http.NewResponseController(w).Flush()
	go s.requestQuit()
}

// requestQuit asks the application to shut down.
func (s *Server) requestQuit() {
	if s.OnQuit != nil {
		s.OnQuit()
	}
}

// requestRestart asks the application to stop and come back up. Where nothing
// is listening it falls back to a plain quit, so the interface's button can
// never leave the user with a window that did nothing.
func (s *Server) requestRestart() {
	if s.OnRestart != nil {
		s.OnRestart()
		return
	}
	s.requestQuit()
}

// Detach makes the application keep running after its last window closes, so
// the agents carry on and can be reattached to later.
func (s *Server) Detach() {
	s.detached.Store(true)
}

// Detached reports whether the application should outlive its windows.
func (s *Server) Detached() bool { return s.detached.Load() }

// Attach reverses a detach, so closing the window quits again.
func (s *Server) Attach() { s.detached.Store(false) }

// Probe checks whether a recorded instance is alive and answering, and returns
// what it reports about itself.
//
// An instance that answers but reports itself not ready is given a while: the
// caller's only other option is to declare the record stale and start a rival
// instance, which splits the agents in two and is much the worse mistake.
func Probe(baseURL, token string) (*healthMsg, error) {
	deadline := time.Now().Add(busyGrace)
	for {
		h, err := probeOnce(baseURL, token)
		if err == nil || !errors.Is(err, ErrNotReady) || !time.Now().Before(deadline) {
			return h, err
		}
		time.Sleep(busyRetry)
	}
}

// ErrNotReady marks the one failure worth waiting out: flockdeck is listening on
// that address, it just cannot answer for its workspace this moment.
//
// It is exported for the launch deciding what an error from Probe means. Every
// other failure says nothing is there; this one says an instance is there and
// busy, and taking it for a stale record would start a rival set of agents
// beside the ones it is busy with.
var ErrNotReady = errors.New("the instance is not ready")

func probeOnce(baseURL, token string) (*healthMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health?t="+token, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// The body is read whatever the status says, because an instance that is
	// merely busy identifies itself in it.
	var h healthMsg
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&h)
	if resp.StatusCode != http.StatusOK {
		if decodeErr == nil && h.App == "flockdeck" {
			return nil, fmt.Errorf("%w: %s", ErrNotReady, resp.Status)
		}
		return nil, fmt.Errorf("instance replied %s", resp.Status)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	if h.App != "flockdeck" {
		return nil, fmt.Errorf("something else is listening on that address")
	}
	return &h, nil
}

// RequestOpen asks a running instance to open a project.
func RequestOpen(baseURL, token, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/open?t="+token+"&path="+queryEscape(path), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return refused("open project", resp)
	}
	return nil
}

// refused is the error for a request the instance turned down. The handlers
// here say why in the reply, and it is the reason rather than the status that
// the person at the command line can act on: "open project: 400 Bad Request"
// sends them looking, where the workspace's own words would not.
func refused(what string, resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if text := strings.TrimSpace(string(msg)); text != "" {
		return fmt.Errorf("%s: %s", what, text)
	}
	return fmt.Errorf("%s: %s", what, resp.Status)
}

// RequestQuit asks a running instance to shut down, and waits for it to have
// gone.
//
// The instance answers as soon as it has accepted the request; stopping every
// agent and saving the layout comes after. Returning on the acceptance made
// `flockdeck -quit` say "stopped" while it was still going, and left the next
// launch racing it: a `flockdeck` typed straight afterwards would probe the
// instance on its way out, find it answering, and attach to a process that was
// about to exit -- ending up with a window onto nothing.
func RequestQuit(baseURL, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/quit?t="+token, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return refused("quit", resp)
	}

	deadline := time.Now().Add(quitGrace)
	for {
		// Only an instance that has stopped answering has gone. One that is
		// merely too busy to report on itself is still there -- and being busy
		// is exactly what shutting down looks like from outside.
		_, err := probeOnce(baseURL, token)
		if err != nil && !errors.Is(err, ErrNotReady) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the instance at %s accepted the request but is still running", baseURL)
		}
		time.Sleep(quitPoll)
	}
}
