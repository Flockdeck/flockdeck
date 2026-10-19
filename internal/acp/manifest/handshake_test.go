package manifest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAdapter is a config that starts this test binary as the fake adapter.
func fakeAdapter(t *testing.T, mode string) HandshakeConfig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return HandshakeConfig{Path: exe, Dir: dir, Env: append(minimalEnv(), fakeEnv+"="+mode), Timeout: 20 * time.Second}
}

func TestHandshakeReadsTheAnswerPastNoise(t *testing.T) {
	res, err := Handshake(context.Background(), fakeAdapter(t, "ok"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ProtocolVersion != 1 || res.AgentName != "fake" || res.AgentVersion != "9.9.9" || strings.Join(res.AuthMethods, ",") != "api-key,oauth" || res.Took <= 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestHandshakeFailures(t *testing.T) {
	cases := map[string]string{
		"error":     "Method not found",
		"v2":        "protocol version 2",
		"noversion": "no protocolVersion",
		"exit":      "fatal: cannot start",
	}
	for mode, want := range cases {
		_, err := Handshake(context.Background(), fakeAdapter(t, mode))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", mode, err, want)
		}
	}
}

func TestHandshakeTimesOutAndEndsTheAdapter(t *testing.T) {
	cfg := fakeAdapter(t, "silent")
	cfg.Timeout = 500 * time.Millisecond
	started := time.Now()
	_, err := Handshake(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("took %v", took)
	}
}

func TestHandshakeRefusesRelativePathsAndNoEnv(t *testing.T) {
	cfg := fakeAdapter(t, "ok")
	rel := cfg
	rel.Path = "adapter"
	if _, err := Handshake(context.Background(), rel); err == nil {
		t.Error("a relative path was accepted")
	}
	rel = cfg
	rel.Dir = "."
	if _, err := Handshake(context.Background(), rel); err == nil {
		t.Error("a relative dir was accepted")
	}
	none := cfg
	none.Env = nil
	if _, err := Handshake(context.Background(), none); err == nil || !strings.Contains(err.Error(), "env is required") {
		t.Errorf("no env: %v", err)
	}
	missing := cfg
	missing.Path = filepath.Join(cfg.Dir, "nothing-here")
	if _, err := Handshake(context.Background(), missing); err == nil {
		t.Error("a missing program started")
	}
}
