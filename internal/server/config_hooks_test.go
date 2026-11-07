package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestConfigHooksAreInPlaceWhenNewReturns covers the callbacks into the program
// being given to New. The server handles requests as soon as New returns and
// reads these without a lock, so assigning them afterwards was a data race.
func TestConfigHooksAreInPlaceWhenNewReturns(t *testing.T) {
	dir := stateTempDir(t)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	goTelemetryOff(t)
	_ = t.TempDir()
	ws, err := workspace.New(workspace.Options{Root: stateTempDir(t)})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	called := map[string]int{}
	cfg := testConfig()
	cfg.OnQuit = func() { called["quit"]++ }
	cfg.OnRestart = func() { called["restart"]++ }
	cfg.OnDetach = func() { called["detach"]++ }
	cfg.OnLastClientGone = func() { called["gone"]++ }
	srv, err := New(ws, cfg)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	srv.OnQuit()
	srv.OnRestart()
	srv.OnDetach()
	srv.OnLastClientGone()
	for _, k := range []string{"quit", "restart", "detach", "gone"} {
		if called[k] != 1 {
			t.Errorf("hook %s from Config was called %d times, want 1", k, called[k])
		}
	}
}
