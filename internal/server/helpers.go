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

	"github.com/jmwri/flockdeck/internal/appwindow"
	"github.com/jmwri/flockdeck/internal/helpers"
)

// Helper apps are run by this instance: the CLI's start, stop and open reach
// it through these endpoints, and the window through control messages (see
// helpersMessage in control.go). Every endpoint is POST, needs the instance
// token in the URL, and refuses anything else, as /quit and /open do.

// openURL opens an address in the user's browser. A variable so a test does
// not start one.
var openURL = appwindow.OpenDefault

// helperStartWait is how long a start request waits for the helper to be
// running. The supervisor's own limits (a banner, then 30 seconds of ready
// checks) are inside it.
const helperStartWait = 90 * time.Second

// SetHelpers gives the server the supervisor and installer for helper apps.
// Until it is called the endpoints answer 503.
func (s *Server) SetHelpers(sup *helpers.Supervisor, inst *helpers.Installer) {
	s.mu.Lock()
	s.helperSup, s.helperInst = sup, inst
	s.mu.Unlock()
}

func (s *Server) helperSupervisor() (*helpers.Supervisor, *helpers.Installer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.helperSup, s.helperInst
}

// helperAction is the shared front of the three endpoints: authorisation,
// method, and the helper id.
func (s *Server) helperAction(w http.ResponseWriter, r *http.Request) (*helpers.Supervisor, string, bool) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", false
	}
	if !requirePost(w, r) || !s.requireURLToken(w, r) {
		return nil, "", false
	}
	sup, _ := s.helperSupervisor()
	if sup == nil {
		http.Error(w, "helper apps are not available in this instance", http.StatusServiceUnavailable)
		return nil, "", false
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "no helper named", http.StatusBadRequest)
		return nil, "", false
	}
	if _, known := helpers.Lookup(id); !known {
		http.Error(w, "not a helper Flockdeck knows", http.StatusBadRequest)
		return nil, "", false
	}
	return sup, id, true
}

func writeStatus(w http.ResponseWriter, st helpers.Status) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}

func (s *Server) handleHelperStart(w http.ResponseWriter, r *http.Request) {
	sup, id, ok := s.helperAction(w, r)
	if !ok {
		return
	}
	if s.helperInstalling(id) {
		http.Error(w, "that helper is being installed; wait for that to finish", http.StatusConflict)
		return
	}
	if _, err := sup.Start(id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), helperStartWait)
	defer cancel()
	writeStatus(w, sup.Wait(ctx, id))
}

func (s *Server) handleHelperStop(w http.ResponseWriter, r *http.Request) {
	sup, id, ok := s.helperAction(w, r)
	if !ok {
		return
	}
	if err := sup.Stop(id); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeStatus(w, sup.Status(id))
}

func (s *Server) handleHelperOpen(w http.ResponseWriter, r *http.Request) {
	sup, id, ok := s.helperAction(w, r)
	if !ok {
		return
	}
	st := sup.Status(id)
	if st.State != helpers.StateRunning || st.URL == "" {
		http.Error(w, fmt.Sprintf("%s is %s, not running; start it first", id, st.State), http.StatusConflict)
		return
	}
	if err := openURL(st.URL); err != nil {
		http.Error(w, "could not open the browser: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeStatus(w, st)
}

// HelperRequest asks the running instance to start, stop or open a helper, and
// returns the helper's status afterwards. It is what the CLI calls.
//
// The token travels in the URL, as /quit's does, and net/http puts the URL in
// the error it returns for a failed connection, which the caller prints. So
// every error that leaves here has the token taken out of it.
func HelperRequest(baseURL, token, action, id string) (helpers.Status, error) {
	timeout := 30 * time.Second
	if action == "start" {
		timeout = helperStartWait + 15*time.Second
	}
	return helperRequest(baseURL, token, action, id, timeout)
}

func helperRequest(baseURL, token, action, id string, timeout time.Duration) (helpers.Status, error) {
	st, err := doHelperRequest(baseURL, token, action, id, timeout)
	if err != nil {
		err = errors.New(redactHelperToken(err.Error(), token))
	}
	return st, err
}

// redactHelperToken removes the token from a message, in the form it is sent
// in and in the escaped form a URL error shows.
func redactHelperToken(msg, token string) string {
	if token == "" {
		return msg
	}
	for _, form := range []string{token, url.QueryEscape(token), url.PathEscape(token)} {
		msg = strings.ReplaceAll(msg, form, "...")
	}
	return msg
}

// helperClient never follows a redirect: the instance does not send one, and
// following one would send the token on to wherever it pointed.
var helperClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func doHelperRequest(baseURL, token, action, id string, timeout time.Duration) (helpers.Status, error) {
	if action != "start" && action != "stop" && action != "open" {
		return helpers.Status{}, fmt.Errorf("unknown helper action %q", action)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	q := url.Values{"t": {token}, "id": {id}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/helpers/"+action+"?"+q.Encode(), nil)
	if err != nil {
		return helpers.Status{}, err
	}
	resp, err := helperClient.Do(req)
	if err != nil {
		return helpers.Status{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return helpers.Status{}, fmt.Errorf("%s", msg)
	}
	var st helpers.Status
	if err := json.Unmarshal(body, &st); err != nil {
		return helpers.Status{}, fmt.Errorf("unreadable answer from the instance: %w", err)
	}
	return st, nil
}
