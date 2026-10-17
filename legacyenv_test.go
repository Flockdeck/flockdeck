package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// captureStderr returns what is written to os.Stderr until the cleanup runs.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	var closed bool
	read := func() string {
		if !closed {
			closed = true
			os.Stderr = old
			_ = w.Close()
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		return buf.String()
	}
	t.Cleanup(func() {
		if !closed {
			read()
		}
		_ = r.Close()
	})
	return read
}

// A pane an earlier build started has only the PERCH_ names. The hook and the
// status line run again on every event and keep their stderr clean, so they
// accept the old name and say nothing about it.
func TestHookAndStatuslineAcceptTheOldTokenWithoutWarning(t *testing.T) {
	t.Setenv("FLOCKDECK_TOKEN", "")
	t.Run("hook", func(t *testing.T) {
		got := make(chan hooks.Event, 1)
		srv, err := hooks.Serve(func(e hooks.Event) { got <- e })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Close() })
		t.Setenv("PERCH_TOKEN", srv.Token())
		read := captureStderr(t)
		var errs bytes.Buffer
		hook([]string{"--endpoint", srv.Endpoint(), "--session", "pane-1", "--event", "Stop"},
			strings.NewReader("{}"), &bytes.Buffer{}, &errs)
		select {
		case e := <-got:
			if e.SessionID != "pane-1" {
				t.Errorf("event = %+v", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the old token was not accepted: %s", errs.String())
		}
		if s := read() + errs.String(); s != "" {
			t.Errorf("the hook wrote %q", s)
		}
	})
	t.Run("statusline", func(t *testing.T) {
		srv, got := usageServer(t)
		t.Setenv("PERCH_TOKEN", srv.Token())
		read := captureStderr(t)
		var out, errs bytes.Buffer
		statusline([]string{"--endpoint", srv.UsageEndpoint(), "--session", "pane-1"},
			strings.NewReader(statusJSON), &out, &errs)
		select {
		case r := <-got:
			if r.Pane != "pane-1" {
				t.Errorf("report = %+v", r)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the old token was not accepted: %s", errs.String())
		}
		if s := read() + errs.String(); s != "" {
			t.Errorf("the status line wrote %q", s)
		}
	})
}

// A command a person runs does say so, once, on its own stderr.
func TestInteractivePaneEnvWarnsOnStderr(t *testing.T) {
	t.Setenv("FLOCKDECK_PANE", "")
	t.Setenv("PERCH_PANE", "old-pane")
	read := captureStderr(t)
	if got := paneEnv("PANE"); got != "old-pane" {
		t.Errorf("paneEnv(PANE) = %q", got)
	}
	paneEnv("PANE")
	out := read()
	if strings.Count(out, "PERCH_PANE") > 1 {
		t.Errorf("warned more than once: %q", out)
	}
	if out != "" && !strings.Contains(out, "FLOCKDECK_PANE") {
		t.Errorf("warning %q does not name the replacement", out)
	}
}
