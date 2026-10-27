package claudeacp

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

// recorder is an acp.Handler that keeps what it is given and answers
// permission requests with the option chosen by pick.
type recorder struct {
	mu      sync.Mutex
	updates []acp.Update
	reqs    []acp.PermissionRequest
	pick    func(acp.PermissionRequest) string
}

func (r *recorder) Update(u acp.Update) {
	r.mu.Lock()
	r.updates = append(r.updates, u)
	r.mu.Unlock()
}

func (r *recorder) Permission(ctx context.Context, req acp.PermissionRequest) string {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	pick := r.pick
	r.mu.Unlock()
	if pick == nil {
		return ""
	}
	return pick(req)
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, u := range r.updates {
		if u.Kind == acp.UpdateAgentMessageChunk && u.Content != nil {
			b.WriteString(u.Content.Text)
		}
	}
	return b.String()
}

// startInterop runs the adapter as a child process, as Flockdeck does, and
// connects the real client to it.
func startInterop(t *testing.T, steps []step, rec *recorder) *acp.Client {
	t.Helper()
	c, _ := startInteropLog(t, steps, rec)
	return c
}

// startInteropLog is startInterop that also returns the path of the file the
// fake claude writes its command line to, one line per start.
func startInteropLog(t *testing.T, steps []step, rec *recorder) (*acp.Client, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env, logPath := fakeEnv(t, steps)
	// the adapter is not the fake: the fake's marker is added again by the
	// adapter when it starts claude
	var adapterEnv []string
	for _, kv := range env {
		if kv != envFake+"=1" {
			adapterEnv = append(adapterEnv, kv)
		}
	}
	env = append(adapterEnv, envAdapt+"=1")
	c, err := acp.Start(acp.Config{Path: exe, Env: env, Dir: acptest.SessionDir(t), Handler: rec, SetupTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, logPath
}

func ctx30(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// The client the app uses can drive the adapter through a turn, a permission
// request and a cancel.
func TestInteropWithTheClient(t *testing.T) {
	rec := &recorder{pick: func(req acp.PermissionRequest) string {
		for _, o := range req.Options {
			if o.Kind == "allow_once" {
				return o.OptionID
			}
		}
		return ""
	}}
	c := startInterop(t, []step{
		expect(`"text":"hello"`), emit(lInit), emit(lMsgStart), emit(textDelta("Hi ")), emit(textDelta("there")), emit(lResultOK),
		expect(`"text":"write"`), emit(lToolUseWrite), emit(lPermWrite), expect(`"request_id":"perm-1"`), emit(lResultOK),
		expect(`"text":"slow"`), emit(lMsgStart), emit(textDelta("working")), expect(`"subtype":"interrupt"`), emit(lResultAborted),
	}, rec)

	init, err := c.Initialize(ctx30(t))
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if init.AgentInfo == nil || init.AgentInfo.Name != "flockdeck-claude" || len(init.AuthMethods) != 0 {
		t.Errorf("initialize = %+v", init)
	}
	sid, err := c.NewSession(ctx30(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	if stop, err := c.Prompt(ctx30(t), sid, "hello"); err != nil || stop != acp.StopEndTurn {
		t.Fatalf("prompt: %q, %v", stop, err)
	}
	if got := rec.text(); got != "Hi there" {
		t.Errorf("text = %q", got)
	}
	rec.mu.Lock()
	var usage *acp.Update
	for i := range rec.updates {
		if rec.updates[i].Kind == acp.UpdateUsage {
			usage = &rec.updates[i]
		}
	}
	rec.mu.Unlock()
	if usage == nil || usage.Used == nil || *usage.Used != 23807 || *usage.Size != 1000000 || usage.Cost == nil || usage.Cost.Amount != 0.00156 {
		t.Errorf("usage = %+v", usage)
	}

	if stop, err := c.Prompt(ctx30(t), sid, "write"); err != nil || stop != acp.StopEndTurn {
		t.Fatalf("write prompt: %q, %v", stop, err)
	}
	rec.mu.Lock()
	if len(rec.reqs) != 1 || rec.reqs[0].ToolCall.ToolCallID != "toolu_W1" || rec.reqs[0].ToolCall.Kind != "edit" {
		t.Errorf("permission requests = %+v", rec.reqs)
	}
	rec.mu.Unlock()

	done := make(chan string, 1)
	go func() {
		stop, err := c.Prompt(ctx30(t), sid, "slow")
		if err != nil {
			stop = "error: " + err.Error()
		}
		done <- stop
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(rec.text(), "working") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Cancel(sid); err != nil {
		t.Fatal(err)
	}
	select {
	case stop := <-done:
		if stop != acp.StopCancelled {
			t.Errorf("cancel: stop = %q", stop)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the cancelled turn did not end")
	}
}

// A restored pane has an id from its last run. Over the real client the adapter
// offers session/resume and not session/load, so that is what the pane calls,
// and claude is started with --resume on that id.
func TestInteropRestoredPaneResumes(t *testing.T) {
	const id = "0b7e1f0e-5c2a-4a3e-9d7a-6f1f3f0b9c11"
	rec := &recorder{}
	c, logPath := startInteropLog(t, []step{
		expect(`"text":"again"`), emit(strings.Replace(lInit, `"session_id":"SID"`, `"session_id":"`+id+`"`, 1)),
		emit(lMsgStart), emit(textDelta("back")), emit(resultWith("back")),
	}, rec)
	if _, err := c.Initialize(ctx30(t)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	caps := c.Capabilities()
	if !caps.Resume || caps.LoadSession {
		t.Fatalf("capabilities = %+v", caps)
	}
	if _, err := c.LoadSession(ctx30(t), id, acptest.SessionDir(t), nil); err == nil {
		t.Error("session/load was accepted")
	}
	got, err := c.ResumeSession(ctx30(t), id, acptest.SessionDir(t), nil)
	if err != nil || got.SessionID != id {
		t.Fatalf("session/resume = %+v, %v", got, err)
	}
	if stop, err := c.Prompt(ctx30(t), id, "again"); err != nil || stop != acp.StopEndTurn {
		t.Fatalf("prompt: %q, %v", stop, err)
	}
	if rec.text() != "back" {
		t.Errorf("text = %q", rec.text())
	}
	var lines []string
	for _, l := range readLog(logPath) {
		if strings.HasPrefix(l, "ARGS:") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "--resume "+id) || strings.Contains(lines[0], "--session-id") {
		t.Errorf("claude was started with %q", lines)
	}
}
