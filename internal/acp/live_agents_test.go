//go:build acp_live

package acp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	testiso "github.com/jmwri/flockdeck/internal/testiso/iso"
)

// TestLiveAgents walks one real agent through what a structured pane does:
// initialize, session/new, a one-word prompt, a permission request for a shell
// command, cancel, and resume in a second client. It is behind the acp_live
// build tag and needs FLOCKDECK_LIVE_AGENT_TESTS=1. It never runs in CI.
//
//	go test -tags acp_live -run TestLiveAgents -v ./internal/acp
//
// Gemini CLI (`gemini --acp`) needs gemini on PATH. Codex runs through
// Flockdeck's own adapter, so it needs codex on PATH and FLOCKDECK_LIVE_BIN
// set to the absolute path of a built flockdeck binary.
//
// The agent gets the real home of the person running the test, because the
// login it uses is kept there, and a new working directory. No key is read,
// asked for or logged; every *_API_KEY variable is dropped from the
// environment, so only a login that is already on the machine can be used.
// Where the agent answers session/new with "authentication required" the test
// stops there and skips: that is the "no login" result, up to the auth step.
//
// The permission request is always answered with a reject option, so the
// command is never run.
func TestLiveAgents(t *testing.T) {
	if os.Getenv("FLOCKDECK_LIVE_AGENT_TESTS") == "" {
		t.Skip("set FLOCKDECK_LIVE_AGENT_TESTS=1 to run real agents")
	}
	cases := []struct {
		name, program string
		args          []string
		viaFlockdeck  bool
	}{
		{"gemini", "gemini", []string{"--acp"}, false},
		{"codex", "codex", []string{"acp-adapter", "codex"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := exec.LookPath(tc.program)
			if err != nil || testiso.IsStub(prog) {
				t.Skipf("%s is not installed", tc.program)
			}
			path := prog
			if tc.viaFlockdeck {
				path = os.Getenv("FLOCKDECK_LIVE_BIN")
				if path == "" {
					t.Skip("FLOCKDECK_LIVE_BIN is not set")
				}
			}
			runLive(t, path, tc.args)
		})
	}
}

func liveEnv(t *testing.T) []string {
	home := testiso.RealHomeDir()
	if home == "" {
		t.Fatal("the real home is not known")
	}
	env := []string{"HOME=" + home, "USERPROFILE=" + home}
	for _, k := range []string{"PATH", "SYSTEMROOT", "SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// liveWait polls until cond holds, for a real agent's pace.
func liveWait(t *testing.T, r *recorder, what string, d time.Duration, cond func([]Update) bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond(r.all()) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runLive(t *testing.T, path string, args []string) {
	dir := acptest.SessionDir(t)
	env := liveEnv(t)
	start := func(r *recorder) *Client {
		c, err := Start(Config{
			Path: path, Args: args, Dir: dir, Env: env, Handler: r,
			SetupTimeout: 2 * time.Minute,
			Stderr:       func(line string) { t.Logf("agent stderr: %s", line) },
		})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	turn := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 2*time.Minute) }

	rec := newRecorder()
	c := start(rec)
	init, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	caps := c.Capabilities()
	t.Logf("initialize ok: agent %+v, auth methods %d, load %v, resume %v, image %v, mcp http %v",
		init.AgentInfo, len(init.AuthMethods), caps.LoadSession, caps.Resume, caps.Prompt.Image, caps.MCP.HTTP)

	created, err := c.NewSessionInfo(ctx, dir, nil)
	if err != nil {
		if IsAuthRequired(err) {
			for _, m := range init.AuthMethods {
				t.Logf("auth method offered: %s (%s)", m.ID, m.Name)
			}
			t.Skipf("no login on this machine: session/new asked for authentication; tested up to the auth step")
		}
		t.Fatalf("session/new: %v", err)
	}
	sid := created.SessionID
	t.Logf("session/new ok: %s, %d config options, %d modes, %d models",
		sid, len(created.ConfigOptions), len(created.Modes.Available), len(created.Models.Available))

	// A one-word prompt that also plants something to ask for after resume.
	pctx, stop := turn()
	reason, err := c.Prompt(pctx, sid, "Remember the word pineapple. Reply with the single word ok and nothing else.")
	stop()
	if err != nil || reason != StopEndTurn {
		t.Fatalf("prompt: stop %q, error %v", reason, err)
	}
	if strings.TrimSpace(rec.text()) == "" {
		t.Error("the one-word prompt produced no text")
	}
	t.Logf("one-word prompt ok: %q", strings.TrimSpace(rec.text()))

	// A shell command must reach the handler as a permission request, with
	// options to choose from, and must not run when it is rejected.
	rec.reset()
	rec.mu.Lock()
	rec.perms = nil
	rec.perm = func(_ context.Context, req PermissionRequest) string {
		for _, o := range req.Options {
			if strings.HasPrefix(o.Kind, "reject") {
				return o.OptionID
			}
		}
		return ""
	}
	rec.mu.Unlock()
	pctx, stop = turn()
	reason, err = c.Prompt(pctx, sid, "Use your shell tool to run exactly this command: echo flockdeck-live-check. Do not do anything else.")
	stop()
	if err != nil {
		t.Fatalf("command prompt: stop %q, error %v", reason, err)
	}
	rec.mu.Lock()
	perms := append([]PermissionRequest(nil), rec.perms...)
	rec.mu.Unlock()
	if len(perms) == 0 {
		t.Errorf("no permission request for a shell command (stop %q, reply %q): the agent ran it without asking, or did not try", reason, strings.TrimSpace(rec.text()))
	} else {
		p := perms[0]
		hasReject := false
		for _, o := range p.Options {
			hasReject = hasReject || strings.HasPrefix(o.Kind, "reject")
		}
		t.Logf("permission card: title %q, kind %q, %d options, has reject %v", p.ToolCall.Title, p.ToolCall.Kind, len(p.Options), hasReject)
		if p.ToolCall.Title == "" && len(p.ToolCall.RawInput) == 0 {
			t.Error("the permission request says nothing about the command")
		}
		if !hasReject {
			t.Error("no reject option offered")
		}
	}

	// Cancel a long turn after its first update.
	rec.reset()
	rec.mu.Lock()
	rec.perm = nil
	rec.mu.Unlock()
	pctx, stop = turn()
	type result struct {
		reason string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := c.Prompt(pctx, sid, "Count from 1 to 400, one number per line, with no other text.")
		done <- result{r, err}
	}()
	liveWait(t, rec, "the first update of a long turn", time.Minute, func(u []Update) bool { return len(u) > 0 })
	if err := c.Cancel(sid); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case r := <-done:
		stop()
		if r.err != nil || r.reason != StopCancelled {
			t.Errorf("cancelled turn: stop %q, error %v, want %q", r.reason, r.err, StopCancelled)
		}
	case <-time.After(time.Minute):
		stop()
		t.Fatal("the turn did not end within a minute of cancel")
	}

	// Resume in a second client, as the runtime does after a restart.
	if !caps.Resume && !caps.LoadSession {
		t.Skip("the agent advertises neither session/resume nor session/load")
	}
	_ = c.Close()
	rec2 := newRecorder()
	c2 := start(rec2)
	if _, err := c2.Initialize(ctx); err != nil {
		t.Fatalf("second initialize: %v", err)
	}
	if caps.Resume {
		_, err = c2.ResumeSession(ctx, sid, dir, nil)
	} else {
		_, err = c2.LoadSession(ctx, sid, dir, nil)
	}
	if err != nil {
		t.Fatalf("resume (resume %v, load %v): %v", caps.Resume, caps.LoadSession, err)
	}
	rec2.reset()
	pctx, stop = turn()
	reason, err = c2.Prompt(pctx, sid, "What word did I ask you to remember? Reply with that one word.")
	stop()
	if err != nil || reason != StopEndTurn {
		t.Fatalf("prompt after resume: stop %q, error %v", reason, err)
	}
	if got := strings.ToLower(rec2.text()); !strings.Contains(got, "pineapple") {
		t.Errorf("after resume the agent answered %q, not pineapple: the history did not come back", strings.TrimSpace(got))
	}
}
