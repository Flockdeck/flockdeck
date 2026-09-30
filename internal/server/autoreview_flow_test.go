package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// gitStatusIn is the PreToolUse payload of an agent running git status in
// dir. The directory matters: auto-review lets a git command through only
// once git itself, asked there, says nothing it would run is configured.
func gitStatusIn(dir string) string {
	d, _ := json.Marshal(dir)
	return `{"session_id":"s","tool_name":"Bash","cwd":` + string(d) + `,"tool_input":{"command":"git status"}}`
}

// cleanRepo is a fresh repository with nothing in its configuration that
// runs a program, under a global and system configuration that are empty too
// -- so what the test's own machine has in ~/.gitconfig cannot change the
// answer.
func cleanRepo(t *testing.T) string {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

// TestAutoReviewAllowsAReadOnlyCommand covers the whole path from a phone or
// a window turning auto-review on to a pane's own PreToolUse hook being told
// to let a command through unasked: the workspace's review handler, wired to
// the hook server in New, only fires once SetPaneAutoReview has turned it on
// for that pane.
func TestAutoReviewAllowsAReadOnlyCommand(t *testing.T) {
	// Emit reads FLOCKDECK_LAUNCH from this process's own environment (see
	// hooks.LaunchEnv), which is set when the test itself happens to be
	// running inside a Flockdeck pane; cleared, it matches what a real hook
	// subprocess reports, which is never this test binary's own launch.
	t.Setenv(hooks.LaunchEnv, "")
	srv, ws := newTestServer(t)
	id := srv.firstPaneID(t)
	hookSrv := ws.HookServer()
	repo := cleanRepo(t)

	stdin := strings.NewReader(gitStatusIn(repo))
	res, err := hooks.Emit(stdin, hookSrv.Endpoint(), hookSrv.Token(), id, "PreToolUse")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if res.Allow {
		t.Fatal("a read-only command was allowed before auto-review was turned on")
	}

	if !ws.SetPaneAutoReview(id, true) {
		t.Fatal("SetPaneAutoReview could not find the pane it was just given")
	}

	stdin = strings.NewReader(gitStatusIn(repo))
	res, err = hooks.Emit(stdin, hookSrv.Endpoint(), hookSrv.Token(), id, "PreToolUse")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !res.Allow {
		t.Fatal("a read-only command was not allowed once auto-review was turned on")
	}
	if res.Reason == "" {
		t.Error("Reason is empty, want the reviewer's own explanation")
	}
}

// TestAutoReviewLeavesAWriteToAsk covers the other half of the promise: even
// with auto-review on, a call that changes something on disk is never
// answered "allow" -- it falls through to Claude Code's own permission
// prompt exactly as it always has.
func TestAutoReviewLeavesAWriteToAsk(t *testing.T) {
	t.Setenv(hooks.LaunchEnv, "") // see TestAutoReviewAllowsAReadOnlyCommand
	srv, ws := newTestServer(t)
	id := srv.firstPaneID(t)
	hookSrv := ws.HookServer()

	if !ws.SetPaneAutoReview(id, true) {
		t.Fatal("SetPaneAutoReview could not find the pane it was just given")
	}

	for _, tc := range []struct{ tool, input string }{
		{"Bash", `{"command":"rm -rf /tmp/x"}`},
		{"Write", `{"file_path":"a.go","content":"package a"}`},
		{"Edit", `{"file_path":"a.go","old_string":"a","new_string":"b"}`},
	} {
		stdin := strings.NewReader(`{"session_id":"s","tool_name":"` + tc.tool + `","tool_input":` + tc.input + `}`)
		res, err := hooks.Emit(stdin, hookSrv.Endpoint(), hookSrv.Token(), id, "PreToolUse")
		if err != nil {
			t.Fatalf("emit %s: %v", tc.tool, err)
		}
		if res.Allow {
			t.Errorf("%s was allowed by auto-review, want it left to ask", tc.tool)
		}
	}
}

// TestAutoReviewCountsWhatItApproves covers AutoApproved, the only thing a
// person turning auto-review on has to see that it is doing anything at all.
func TestAutoReviewCountsWhatItApproves(t *testing.T) {
	t.Setenv(hooks.LaunchEnv, "") // see TestAutoReviewAllowsAReadOnlyCommand
	srv, ws := newTestServer(t)
	id := srv.firstPaneID(t)
	hookSrv := ws.HookServer()
	ws.SetPaneAutoReview(id, true)
	repo := cleanRepo(t)

	for range 3 {
		stdin := strings.NewReader(gitStatusIn(repo))
		if _, err := hooks.Emit(stdin, hookSrv.Endpoint(), hookSrv.Token(), id, "PreToolUse"); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	if p := ws.Pane(id); p == nil || p.AutoApproved != 3 {
		got := -1
		if p != nil {
			got = p.AutoApproved
		}
		t.Errorf("AutoApproved = %d, want 3", got)
	}
}
