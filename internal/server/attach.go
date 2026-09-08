package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// healthMsg is what a second launch reads to decide whether the recorded
// instance is really alive.
type healthMsg struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
	Projects int    `json:"projects"`
}

// Version is reported by the health endpoint. main sets it at startup.
var Version = "dev"

// probeTimeout is how long a second launch waits for an answer to /health.
// healthTimeout, the wait for the workspace goroutine behind it, is
// deliberately shorter: an instance whose port answers but whose workspace has
// wedged should say so, rather than let the probe give up on its own side with
// nothing to report but a deadline.
const (
	probeTimeout  = 2 * time.Second
	healthTimeout = probeTimeout / 2
)

// handleHealth answers a probe from another launch of the binary.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
		// window it expected would never appear; failing the probe sends it
		// off to start its own instead.
		http.Error(w, "workspace is not responding", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthMsg{
		App: "agent-wrapper", Version: Version, PID: pid(), Projects: projects,
	})
}

// handleOpen lets a second launch hand its directory to the running instance,
// so `agent-wrapper -C somewhere` attaches and opens that project rather than
// starting a rival server.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) {
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
// `agent-wrapper -quit` reaches a detached one.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !requirePost(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
func Probe(baseURL, token string) (*healthMsg, error) {
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instance replied %s", resp.Status)
	}
	var h healthMsg
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	if h.App != "agent-wrapper" {
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
