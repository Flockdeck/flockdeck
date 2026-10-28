package codexacp

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
// permission requests with the allow_once option.
type recorder struct {
	mu      sync.Mutex
	updates []acp.Update
	reqs    []acp.PermissionRequest
}

func (r *recorder) Update(u acp.Update) {
	r.mu.Lock()
	r.updates = append(r.updates, u)
	r.mu.Unlock()
}

func (r *recorder) Permission(ctx context.Context, req acp.PermissionRequest) string {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	for _, o := range req.Options {
		if o.Kind == "allow_once" {
			return o.OptionID
		}
	}
	return ""
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

// The client the app uses can drive the adapter through a turn, a permission
// request and a cancel. The adapter runs as a child process, as in the app.
func TestInteropWithTheClient(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env, _ := fakeEnv(t, []step{
		expect(`"text":"hello"`), startedTurn(), delta("m1", "Hi "), delta("m1", "there"),
		note("thread/tokenUsage/updated", `{"threadId":"thr-1","turnId":"turn-1","tokenUsage":{"total":{"totalTokens":5},"last":{"totalTokens":5},"modelContextWindow":1000}}`),
		endTurn("completed"),
		expect(`"text":"run it"`), startedTurn(), cmdItem("item/started", "c1", "echo hi", "inProgress"),
		cmdApproval("ap-1", "c1", "echo hi", ""), answered("ap-1", `{"decision":"accept"}`), endTurn("completed"),
		expect(`"text":"slow"`), startedTurn(), delta("m2", "working"),
		expect(`"method":"turn/interrupt"`), endTurn("interrupted"),
	})
	var adapterEnv []string
	for _, kv := range env {
		if kv != envFake+"=1" {
			adapterEnv = append(adapterEnv, kv)
		}
	}
	rec := &recorder{}
	c, err := acp.Start(acp.Config{Path: exe, Env: append(adapterEnv, envAdapt+"=1"), Dir: acptest.SessionDir(t), Handler: rec, SetupTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	init, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if init.AgentInfo == nil || init.AgentInfo.Name != "flockdeck-codex" || len(init.AuthMethods) != 1 || init.AuthMethods[0].ID != "flockdeck-login" {
		t.Errorf("initialize = %+v", init)
	}
	sid, err := c.NewSession(ctx, acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if stop, err := c.Prompt(ctx, sid, "hello"); err != nil || stop != acp.StopEndTurn {
		t.Fatalf("prompt: %q, %v", stop, err)
	}
	if got := rec.text(); got != "Hi there" {
		t.Errorf("text = %q", got)
	}
	if stop, err := c.Prompt(ctx, sid, "run it"); err != nil || stop != acp.StopEndTurn {
		t.Fatalf("run prompt: %q, %v", stop, err)
	}
	rec.mu.Lock()
	if len(rec.reqs) != 1 || rec.reqs[0].ToolCall.ToolCallID != "c1" || rec.reqs[0].ToolCall.Kind != "execute" {
		t.Errorf("permission requests = %+v", rec.reqs)
	}
	var usage *acp.Update
	for i := range rec.updates {
		if rec.updates[i].Kind == acp.UpdateUsage {
			usage = &rec.updates[i]
		}
	}
	rec.mu.Unlock()
	if usage == nil || usage.Used == nil || *usage.Used != 5 || *usage.Size != 1000 || usage.Cost != nil {
		t.Errorf("usage = %+v", usage)
	}

	done := make(chan string, 1)
	go func() {
		stop, err := c.Prompt(ctx, sid, "slow")
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
