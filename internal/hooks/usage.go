package hooks

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jmwri/flockdeck/internal/spend"
)

// UsageReport is what a pane posts to say what its agent has spent: one call's
// tokens from Flockdeck's own chat client, or the running totals and the
// subscription's windows that Claude Code hands its status line.
//
// It goes to a route of its own rather than riding a lifecycle event, because
// it is not one: it changes nothing about the pane's status, and the events
// are Claude Code's own names, which this is not.
type UsageReport struct {
	spend.Report
	Token string `json:"token"`
}

// SetUsageHandler installs the function a pane's spending is delivered to.
func (s *Server) SetUsageHandler(fn func(spend.Report)) {
	s.mu.Lock()
	s.onUsage = fn
	s.mu.Unlock()
}

// UsageEndpoint is the URL panes post their spending to.
func (s *Server) UsageEndpoint() string { return s.BaseURL() + "/usage" }

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var rep UsageReport
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&rep); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// The same secret the lifecycle events carry: the port is loopback-only
	// but any local process can reach it, and a figure written by one of those
	// would be shown in a pane as though its agent had said it.
	if subtle.ConstantTimeCompare([]byte(rep.Token), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.mu.RLock()
	fn := s.onUsage
	s.mu.RUnlock()
	if fn != nil && rep.Pane != "" {
		fn(rep.Report)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Report is the client half: it posts one report and gives up after timeout.
//
// It is sent from inside an agent's own loop -- after an API call, or on every
// tick of Claude Code's status line -- so it must never hold that loop up. A
// Flockdeck that is not there loses the figure, and nothing else.
func Report(endpoint, token string, r spend.Report, timeout time.Duration) error {
	body, err := json.Marshal(UsageReport{Report: r, Token: token})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the application refused the usage report: %s", resp.Status)
	}
	return nil
}
