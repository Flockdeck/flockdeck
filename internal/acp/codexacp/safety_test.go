package codexacp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// answered is what the fake codex sees as the answer to a request with this id.
func answered(id, result string) step { return expect(`{"id":"` + id + `","result":` + result + `}`) }

func permOptions(m rpcMsg) []string {
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
		} `json:"options"`
	}
	_ = json.Unmarshal(m.Params, &p)
	var out []string
	for _, o := range p.Options {
		out = append(out, o.OptionID)
	}
	return out
}

func TestCommandApproval(t *testing.T) {
	for name, tc := range map[string]struct {
		answer any
		want   string
	}{
		"allow":     {outcome("allow"), `{"decision":"accept"}`},
		"reject":    {outcome("reject"), `{"decision":"decline"}`},
		"cancelled": {cancelledOutcome(), `{"decision":"cancel"}`},
		"error":     {&rpcErr{Code: -32000, Message: "no"}, `{"decision":"cancel"}`},
		"unknown":   {outcome("something-else"), `{"decision":"decline"}`},
		"garbage":   {map[string]any{"x": 1}, `{"decision":"cancel"}`},
		"always":    {outcome("allow_always"), `{"decision":"accept"}`},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, []step{
				expect(`"method":"turn/start"`), startedTurn(),
				cmdItem("item/started", "c1", "rm -rf build", "inProgress"),
				cmdApproval("ap-1", "c1", "rm -rf build", `,"reason":"cleanup"`),
				answered("ap-1", tc.want),
				endTurn("completed"),
			})
			h.onReq = func(m rpcMsg) any { return tc.answer }
			sid := h.start()
			if stopReason(t, newText(h, sid)) != "end_turn" {
				t.Fatal("bad stop")
			}
			req := h.requests("session/request_permission")
			if len(req) != 1 {
				t.Fatalf("%d permission requests", len(req))
			}
			if opts := permOptions(req[0]); strings.Join(opts, ",") != "allow,allow_always,reject" {
				t.Errorf("options = %v", opts)
			}
			if !strings.Contains(string(req[0].Params), `"toolCallId":"c1"`) || !strings.Contains(string(req[0].Params), "rm -rf build") {
				t.Errorf("card = %s", req[0].Params)
			}
		})
	}
}

func TestAllowAlwaysIsForTheSameCommandOnly(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		cmdApproval("ap-1", "c1", "go test", ""),
		answered("ap-1", `{"decision":"accept"}`),
		cmdApproval("ap-2", "c2", "go test", ""), // the same command: no card
		answered("ap-2", `{"decision":"accept"}`),
		cmdApproval("ap-3", "c3", "go test -v", ""), // another command: a card
		answered("ap-3", `{"decision":"decline"}`),
		cmdApproval("ap-4", "c4", "go test", `,"networkApprovalContext":{"host":"evil.test"}`), // network: a card, no always
		answered("ap-4", `{"decision":"decline"}`),
		cmdApproval("ap-5", "c5", "go test", `,"someNewField":{"x":1}`), // unknown member: a card
		answered("ap-5", `{"decision":"decline"}`),
		endTurn("completed"),
	})
	var n int
	var mu sync.Mutex
	h.onReq = func(m rpcMsg) any {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n == 1 {
			return outcome("allow_always")
		}
		return outcome("reject")
	}
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	reqs := h.requests("session/request_permission")
	if len(reqs) != 4 {
		t.Fatalf("%d cards, want 4 (the repeat is not asked)", len(reqs))
	}
	if opts := permOptions(reqs[2]); strings.Contains(strings.Join(opts, ","), "always") {
		t.Errorf("a network request offers always: %v", opts)
	}
	if opts := permOptions(reqs[3]); strings.Contains(strings.Join(opts, ","), "always") {
		t.Errorf("a request with an unknown member offers always: %v", opts)
	}
	if logHas(readLog(h.log), "acceptForSession") {
		t.Error("acceptForSession was sent: the rule belongs to the adapter")
	}
}

func TestAllowAlwaysIsNeverOfferedForFilesOrPermissions(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		emit(`{"id":"ap-1","method":"item/fileChange/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"f1","reason":"write","grantRoot":"/etc"}}`),
		answered("ap-1", `{"decision":"decline"}`),
		emit(`{"id":"ap-2","method":"item/fileChange/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"f1"}}`),
		answered("ap-2", `{"decision":"accept"}`),
		emit(`{"id":"ap-3","method":"item/permissions/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"p1","cwd":"/work","reason":"net","permissions":{"network":{"enabled":true}}}}`),
		answered("ap-3", `{"permissions":{"network":{"enabled":true}},"scope":"turn"}`),
		emit(`{"id":"ap-4","method":"item/permissions/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"p1","permissions":{"network":{"enabled":true}}}}`),
		answered("ap-4", `{"permissions":{},"scope":"turn"}`),
		endTurn("completed"),
	})
	var n int
	var mu sync.Mutex
	h.onReq = func(m rpcMsg) any {
		mu.Lock()
		defer mu.Unlock()
		n++
		switch n {
		case 1:
			return outcome("allow_always") // asked for, never offered: refused
		case 4:
			return outcome("reject")
		}
		return outcome("allow")
	}
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	reqs := h.requests("session/request_permission")
	if len(reqs) != 4 {
		t.Fatalf("%d cards, want 4: each is asked", len(reqs))
	}
	for _, r := range reqs {
		if o := permOptions(r); strings.Join(o, ",") != "allow,reject" {
			t.Errorf("options = %v", o)
		}
	}
	if !strings.Contains(string(reqs[0].Params), "/etc") {
		t.Errorf("the grant root is not on the card: %s", reqs[0].Params)
	}
}

func TestRequestsThatAreNotUnderstoodAreRefused(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		emit(`{"id":"q-1","method":"item/tool/requestUserInput","params":{"threadId":"thr-1","questions":[]}}`),
		expect(`"id":"q-1"}`),
		emit(`{"id":"ap-1","method":"item/commandExecution/requestApproval","params":{"threadId":"other","turnId":"t","itemId":"c1","command":"id"}}`),
		answered("ap-1", `{"decision":"decline"}`),
		emit(`{"id":"ap-2","method":"item/commandExecution/requestApproval","params":{"threadId":"thr-1","turnId":"t","command":"id"}}`),
		answered("ap-2", `{"decision":"decline"}`),
		emit(`{"id":"ap-3","method":"applyPatchApproval","params":{"conversationId":"thr-1"}}`),
		expect(`"id":"ap-3"}`),
		endTurn("completed"),
	})
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	if n := len(h.requests("session/request_permission")); n != 0 {
		t.Errorf("%d cards for requests that must be refused", n)
	}
	if logHas(readLog(h.log), "MISSING") {
		t.Error("an answer was not sent")
	}
}

func TestCancelInterruptsAndRefusesWaitingApprovals(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		cmdApproval("ap-1", "c1", "make", ""),
		expect(`"method":"turn/interrupt"`),
		endTurn("interrupted"),
	})
	release := make(chan struct{})
	h.onReq = func(m rpcMsg) any { <-release; return outcome("allow") }
	sid := h.start()
	p1 := h.promptText(sid, "one")
	p2 := h.promptText(sid, "two") // queued behind the first
	h.waitFor(func(m rpcMsg) bool { return m.Method == "session/request_permission" }, 10*time.Second)
	h.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": sid}})
	if got := stopReason(t, h.await(p1)); got != "cancelled" {
		t.Errorf("first = %s", got)
	}
	if got := stopReason(t, h.await(p2)); got != "cancelled" {
		t.Errorf("queued = %s", got)
	}
	close(release) // the client answers allow after the cancel: it must not reach codex
	time.Sleep(200 * time.Millisecond)
	log := readLog(h.log)
	intr := logFind(log, `"method":"turn/interrupt"`)
	if !strings.Contains(intr, `"turnId":"turn-1"`) || !strings.Contains(intr, `"threadId":"thr-1"`) {
		t.Errorf("interrupt = %s", intr)
	}
	if !logHas(log, `{"id":"ap-1","result":{"decision":"cancel"}}`) || logHas(log, `"decision":"accept"`) {
		t.Errorf("the waiting approval was not refused, or was allowed late: %v", log)
	}
	if logCount(log, `"method":"turn/start"`) != 1 {
		t.Error("a cancelled queued prompt was sent")
	}
}

func logCount(log []string, sub string) int {
	n := 0
	for _, l := range log {
		if strings.HasPrefix(l, "IN: ") && strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

func TestCancelBeforeTheTurnHasAnID(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`),
		sleep(400), // the turn id is not known to the adapter yet
		startedTurn(),
		expect(`"method":"turn/interrupt"`),
		endTurn("interrupted"),
	}, hopts{extraEnv: []string{envNoTurn + "=500"}})
	sid := h.start()
	p := h.promptText(sid, "go")
	waitLog(t, h.log, `"method":"turn/start"`)
	h.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": sid}})
	if got := stopReason(t, h.await(p)); got != "cancelled" {
		t.Errorf("stop = %s", got)
	}
	if !strings.Contains(logFind(readLog(h.log), `"method":"turn/interrupt"`), `"turnId":"turn-1"`) {
		t.Error("the held cancel was not sent when the id arrived")
	}
}

func TestAnInterruptThatDoesNotEndTheTurnEndsCodex(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		expect(`"method":"turn/interrupt"`),
		sleep(60000), // ignores the interrupt
	}, hopts{grace: 500 * time.Millisecond})
	sid := h.start()
	p := h.promptText(sid, "go")
	waitLog(t, h.log, `"method":"turn/start"`)
	h.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": sid}})
	r := h.await(p)
	if r.Error == nil && stopReason(t, r) != "cancelled" {
		t.Errorf("result = %+v", r)
	}
	pid := fakePID(t, h.log)
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		endProcess(pid)
		t.Error("codex was left running")
	}
}

func TestCodexIsKilledWhenTheInputEndsAndIgnoresIt(t *testing.T) {
	h := newHarness(t, []step{sleep(60000)})
	h.start()
	pid := fakePID(t, h.log)
	h.shutdown()
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		endProcess(pid)
		t.Fatal("codex outlived the adapter")
	}
}

func TestRunStopsWhenTheContextIsDoneAndTheInputIsOpen(t *testing.T) {
	env, logPath := fakeEnv(t, []step{sleep(60000)})
	exe, _ := os.Executable()
	inR, inW := io.Pipe()
	defer inW.Close()
	outR, outW := io.Pipe()
	go io.Copy(io.Discard, outR)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, inR, outW, Options{CodexPath: exe, Env: env, Stderr: io.Discard}) }()
	go func() {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{"cwd": os.TempDir()}})
		inW.Write(append(b, '\n'))
	}()
	waitLog(t, logPath, `"method":"thread/start"`)
	time.Sleep(200 * time.Millisecond)
	pid := fakePID(t, logPath)
	cancel() // what SIGTERM does
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		endProcess(pid)
		t.Fatal("Run did not return")
	}
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		endProcess(pid)
		t.Error("codex outlived the adapter")
	}
}

func TestErrorsNeverCarryCodexText(t *testing.T) {
	const secret = "SECRET-codex-detail"
	run := func(t *testing.T, o hopts, steps []step, do func(h *harness)) *harness {
		h := newHarness(t, steps, o)
		do(h)
		if all := h.everything(); strings.Contains(all, secret) || strings.Contains(all, "SECRET-stderr") {
			t.Errorf("codex text reached the client: %s", all)
		}
		return h
	}
	t.Run("initialize fails", func(t *testing.T) {
		run(t, hopts{extraEnv: []string{envErr + "=initialize", envStderr + "=SECRET-stderr"}}, nil, func(h *harness) {
			h.call("initialize", map[string]any{"protocolVersion": 1})
			r := h.call("session/new", map[string]any{"cwd": h.cwd})
			if r.Error == nil || r.Error.Message != unavailable {
				t.Errorf("%+v", r.Error)
			}
		})
	})
	t.Run("thread/start fails", func(t *testing.T) {
		run(t, hopts{extraEnv: []string{envErr + "=thread/start"}}, nil, func(h *harness) {
			h.call("initialize", map[string]any{"protocolVersion": 1})
			r := h.call("session/new", map[string]any{"cwd": h.cwd})
			if r.Error == nil || r.Error.Message != unavailable {
				t.Errorf("%+v", r.Error)
			}
		})
	})
	t.Run("no such executable", func(t *testing.T) {
		run(t, hopts{codexPath: "no-such-codex-" + secret}, nil, func(h *harness) {
			h.call("initialize", map[string]any{"protocolVersion": 1})
			r := h.call("session/new", map[string]any{"cwd": h.cwd})
			if r.Error == nil || r.Error.Message != unavailable {
				t.Errorf("%+v", r.Error)
			}
		})
	})
	t.Run("turn/start fails", func(t *testing.T) {
		run(t, hopts{extraEnv: []string{envErr + "=turn/start"}}, nil, func(h *harness) {
			sid := h.start()
			r := h.await(h.promptText(sid, "go"))
			if r.Error == nil || strings.Contains(r.Error.Message, secret) {
				t.Errorf("%+v", r.Error)
			}
		})
	})
	t.Run("turn fails", func(t *testing.T) {
		steps := []step{expect(`"method":"turn/start"`), startedTurn(),
			note("turn/completed", `{"threadId":"thr-1","turn":{"id":"turn-1","status":"failed","error":{"message":"`+secret+`","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":500}}}}}`)}
		run(t, hopts{}, steps, func(h *harness) {
			sid := h.start()
			r := h.await(h.promptText(sid, "go"))
			if r.Error == nil || r.Error.Message != "the turn failed" {
				t.Errorf("%+v", r.Error)
			}
		})
	})
	t.Run("not logged in", func(t *testing.T) {
		steps := []step{expect(`"method":"turn/start"`), startedTurn(),
			note("turn/completed", `{"threadId":"thr-1","turn":{"id":"turn-1","status":"failed","error":{"message":"`+secret+`","codexErrorInfo":"unauthorized"}}}`)}
		run(t, hopts{}, steps, func(h *harness) {
			sid := h.start()
			r := h.await(h.promptText(sid, "go"))
			if r.Error == nil || r.Error.Code != codeAuthRequired || r.Error.Message != "Authentication required" {
				t.Errorf("%+v", r.Error)
			}
		})
	})
	t.Run("context window", func(t *testing.T) {
		steps := []step{expect(`"method":"turn/start"`), startedTurn(),
			note("turn/completed", `{"threadId":"thr-1","turn":{"id":"turn-1","status":"failed","error":{"message":"`+secret+`","codexErrorInfo":"contextWindowExceeded"}}}`)}
		run(t, hopts{}, steps, func(h *harness) {
			sid := h.start()
			if got := stopReason(t, h.await(h.promptText(sid, "go"))); got != "max_tokens" {
				t.Errorf("stop = %s", got)
			}
		})
	})
	t.Run("codex exits mid-turn", func(t *testing.T) {
		run(t, hopts{extraEnv: []string{envStderr + "=SECRET-stderr"}}, []step{expect(`"method":"turn/start"`), startedTurn(), exitWith(3)}, func(h *harness) {
			sid := h.start()
			r := h.await(h.promptText(sid, "go"))
			if r.Error == nil || r.Error.Message != "the agent stopped during the turn" {
				t.Errorf("%+v", r.Error)
			}
			r = h.await(h.promptText(sid, "again"))
			if r.Error == nil {
				t.Error("a prompt after the exit succeeded")
			}
		})
	})
}

func TestEnvironmentLeavesFlockdeckOut(t *testing.T) {
	h := newHarness(t, nil, hopts{extraEnv: []string{
		"FLOCKDECK_TOKEN=a", "flockdeck_lower=b", "Flockdeck_Mixed=c", "PERCH_X=d", "OPENAI_API_KEY=keep", "CODEX_HOME=keep2",
	}})
	h.start()
	var envs []string
	for _, l := range readLog(h.log) {
		if strings.HasPrefix(l, "ENV: ") {
			envs = append(envs, strings.ToUpper(l))
		}
	}
	joined := strings.Join(envs, "\n")
	for _, bad := range []string{"FLOCKDECK_TOKEN", "FLOCKDECK_LOWER", "FLOCKDECK_MIXED", "PERCH_X"} {
		if strings.Contains(joined, bad) {
			t.Errorf("%s reached codex", bad)
		}
	}
	for _, good := range []string{"OPENAI_API_KEY=KEEP", "CODEX_HOME=KEEP2"} {
		if !strings.Contains(joined, good) {
			t.Errorf("%s was dropped", good)
		}
	}
}

func TestCleanEnvAlsoCleansAnExplicitEnv(t *testing.T) {
	o := Options{Env: []string{"FLOCKDECK_A=1", "flockdeck_b=2", "X=3"}}
	o.defaults()
	if len(o.Env) != 1 || o.Env[0] != "X=3" {
		t.Errorf("env = %v", o.Env)
	}
}

func TestALineFromCodexOverTheLimitEndsTheSession(t *testing.T) {
	h := newHarness(t, []step{expect(`"method":"turn/start"`), startedTurn(), big(200 << 10), sleep(60000)}, hopts{maxCodex: 64 << 10})
	sid := h.start()
	r := h.await(h.promptText(sid, "go"))
	if r.Error == nil {
		t.Errorf("the prompt did not fail: %+v", r)
	}
	pid := fakePID(t, h.log)
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		endProcess(pid)
		t.Error("codex was left running")
	}
}

func TestALineFromTheClientOverTheLimitStopsTheAdapter(t *testing.T) {
	h := newHarness(t, nil, hopts{maxClient: 4 << 10})
	sid := h.start()
	h.write(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/prompt", "params": map[string]any{"sessionId": sid, "prompt": []any{
		map[string]any{"type": "text", "text": strings.Repeat("a", 10<<10)},
	}}})
	select {
	case <-h.stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the adapter did not stop")
	}
	if h.runErr == nil {
		t.Error("Run returned nil for a line over the limit")
	}
	pid := fakePID(t, h.log)
	for i := 0; i < 100 && alive(pid); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		endProcess(pid)
		t.Error("codex was left running")
	}
}

func TestReadLinesBound(t *testing.T) {
	var got []string
	err := readLines(strings.NewReader("a\n\n  \nbb\nccc"), 8, func(b []byte) { got = append(got, strings.TrimSpace(string(b))) })
	if err != nil || strings.Join(got, ",") != "a,bb,ccc" {
		t.Errorf("%v %v", got, err)
	}
	if err := readLines(strings.NewReader(strings.Repeat("x", 100)+"\n"), 50, func([]byte) {}); err != errLineTooLong {
		t.Errorf("err = %v", err)
	}
	// the limit counts the line, not the buffer: a long line made of many reads
	if err := readLines(strings.NewReader(strings.Repeat("x", 200<<10)+"\n"), 100<<10, func([]byte) {}); err != errLineTooLong {
		t.Errorf("err = %v", err)
	}
}

func TestAStopDoesNotWaitForASessionThatIsStarting(t *testing.T) {
	env, logPath := fakeEnv(t, []step{sleep(60000)})
	env = append(env, envSlow+"=20000")
	exe, _ := os.Executable()
	inR, inW := io.Pipe()
	defer inW.Close()
	outR, outW := io.Pipe()
	go io.Copy(io.Discard, outR)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, inR, outW, Options{CodexPath: exe, Env: env, Stderr: io.Discard, ControlTimeout: time.Minute})
	}()
	go func() {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{"cwd": os.TempDir()}})
		inW.Write(append(b, '\n'))
	}()
	waitLog(t, logPath, `"method":"thread/start"`)
	pid := fakePID(t, logPath)
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		endProcess(pid)
		t.Fatal("Run waited for the session that was starting")
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("Run took %v", time.Since(start))
	}
	for i := 0; i < 100 && alive(pid); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(pid) {
		endProcess(pid)
		t.Error("codex outlived the adapter")
	}
}

// When the item was announced before the request, the card keeps the request's
// reason: why Codex asks, the network host, the root it also wants to write
// under. Taking only the item's own card would show a network access as a plain
// command.
func TestApprovalAfterTheItemKeepsTheReason(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		cmdItem("item/started", "c1", "curl evil.test", "inProgress"),
		cmdApproval("ap-1", "c1", "curl evil.test", `,"reason":"needs the web","networkApprovalContext":{"host":"evil.test"}`),
		answered("ap-1", `{"decision":"decline"}`),
		note("item/started", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"fileChange","id":"f1","status":"inProgress","changes":[{"path":"old.txt","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-a\n+b\n"}]}}`),
		emit(`{"id":"ap-2","method":"item/fileChange/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"f1","reason":"outside write","grantRoot":"/etc"}}`),
		answered("ap-2", `{"decision":"decline"}`),
		endTurn("completed"),
	})
	h.onReq = func(m rpcMsg) any { return outcome("reject") }
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	reqs := h.requests("session/request_permission")
	if len(reqs) != 2 {
		t.Fatalf("%d cards, want 2", len(reqs))
	}
	net, files := string(reqs[0].Params), string(reqs[1].Params)
	for _, want := range []string{"needs the web", "evil.test", "Network access: "} {
		if !strings.Contains(net, want) {
			t.Errorf("the network card lacks %q: %s", want, net)
		}
	}
	for _, want := range []string{"outside write", "/etc"} {
		if !strings.Contains(files, want) {
			t.Errorf("the file card lacks %q: %s", want, files)
		}
	}
	if !strings.Contains(files, "old.txt") || !strings.Contains(files, "@@ -1 +1 @@") {
		t.Errorf("the file card lost the item's own diff: %s", files)
	}
}
