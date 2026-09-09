package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	busyGrace     = 5 * time.Second
	busyRetry     = 250 * time.Millisecond
)

// handleHealth answers a probe from another launch of the binary.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requireNotAPage(w, r) {
		return
	}
	done := make(chan int, 1)
	s.do(func() { done <- len(s.ws.Projects()) })

	var projects int
	select {
	case projects = <-done:
	case <-s.closed:
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	case <-time.After(healthTimeout):
		// The port is still answering but the workspace behind it is not.
		// Reporting a made-up project count would tell the second launch to
		// hand its directory to an instance that cannot open it, and the
		// window it expected would never appear. So this fails -- but it
		// still says who it is, so the launch can wait for the workspace to
		// come back instead of writing the instance off.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(healthMsg{
			App: "perch", Version: Version, PID: pid(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthMsg{
		App: "perch", Version: Version, PID: pid(), Projects: projects, Ready: true,
	})
}

// handleOpen lets a second launch hand its directory to the running instance,
// so `perch -C somewhere` attaches and opens that project rather than
// starting a rival server.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) || !requireNotAPage(w, r) {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "no path", http.StatusBadRequest)
		return
	}
	errc := make(chan error, 1)
	s.do(func() {
		err := s.ws.OpenProject(path)
		if err == nil {
			s.Wake()
		}
		errc <- err
	})
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
	case <-time.After(10 * time.Second):
		http.Error(w, "timed out", http.StatusGatewayTimeout)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireNotAPage rejects a request made by a page in a browser.
//
// These three endpoints are for another launch of the binary: they report on
// the instance, hand it a project, or shut it down. The window's own page
// never calls any of them, and it matters that nothing else can. The token
// they are gated on is held in a cookie, cookies are scoped to a host and take
// no notice of the port, and a different port is not a different site -- so a
// page served from anything else on 127.0.0.1 or localhost has this
// instance's token attached to a request it makes here, and SameSite does not
// hold it back. A plain cross-origin POST needs no permission to be sent; that
// its reply cannot be read is no comfort when the request itself stops every
// agent the user has running.
//
// A browser announces itself by sending Origin. Nothing without one is a page,
// and a page from this server's own address is allowed in case one ever has
// reason to call these.
func requireNotAPage(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if u, err := url.Parse(origin); err == nil && strings.EqualFold(u.Host, r.Host) {
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
// `perch -quit` reaches a detached one.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) || !requireNotAPage(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
	// The answer has to be on the wire before the shutdown starts. Quitting
	// ends the process, and a reply still sitting in the connection's buffer
	// dies with it -- leaving `perch -quit` to report a broken connection for
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
		if err == nil || !errors.Is(err, errNotReady) || !time.Now().Before(deadline) {
			return h, err
		}
		time.Sleep(busyRetry)
	}
}

// errNotReady marks the one failure worth waiting out: perch is listening on
// that address, it just cannot answer for its workspace this moment.
var errNotReady = errors.New("the instance is not ready")

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
		if decodeErr == nil && h.App == "perch" {
			return nil, fmt.Errorf("%w: %s", errNotReady, resp.Status)
		}
		return nil, fmt.Errorf("instance replied %s", resp.Status)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	if h.App != "perch" {
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
		return fmt.Errorf("open project: %s", resp.Status)
	}
	return nil
}

// RequestQuit asks a running instance to shut down.
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
		return fmt.Errorf("quit: %s", resp.Status)
	}
	return nil
}
