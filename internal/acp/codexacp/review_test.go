package codexacp

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestCleanEnvIsNeverNil(t *testing.T) {
	if got := cleanEnv([]string{"FLOCKDECK_A=1", "perch_b=2"}); got == nil || len(got) != 0 {
		t.Errorf("cleanEnv = %#v: a nil Env would hand the child the parent's environment", got)
	}
	o := Options{Env: []string{"FLOCKDECK_A=1"}}
	o.defaults()
	if o.Env == nil || len(o.Env) != 0 {
		t.Errorf("Env = %#v", o.Env)
	}
}

func TestAnExtraPermissionsRequestLongerThanTheCardIsNotGranted(t *testing.T) {
	long := `{"fs":{"write":["` + strings.Repeat("a", 3000) + `"]}}`
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		emit(`{"id":"ap-1","method":"item/permissions/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"p1","permissions":` + long + `}}`),
		answered("ap-1", `{"permissions":{},"scope":"turn"}`),
		endTurn("completed"),
	})
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	if logHas(readLog(h.log), "MISSING") {
		t.Error("the request was granted, or not answered")
	}
}

func TestAFileChangeLargerThanTheCardIsNotApproved(t *testing.T) {
	var changes []string
	for i := 0; i < maxChanges+3; i++ {
		changes = append(changes, `{"path":"f`+itoa(i)+`.txt","kind":{"type":"add"},"diff":"x"}`)
	}
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		note("item/started", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"fileChange","id":"f1","status":"inProgress","changes":[`+strings.Join(changes, ",")+`]}}`),
		emit(`{"id":"ap-1","method":"item/fileChange/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"f1"}}`),
		answered("ap-1", `{"decision":"decline"}`),
		endTurn("completed"),
	})
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	if n := len(h.requests("session/request_permission")); n != 0 {
		t.Errorf("%d cards for a change the card cannot show", n)
	}
	if !strings.Contains(toJSON(h.updates()), "3 more files are not shown") {
		t.Error("the call does not say that files are missing")
	}
}

func TestATurnEndWithNoIDOrNoThreadDoesNotEndThePrompt(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		note("turn/completed", `{"threadId":"thr-1","turn":{"status":"completed"}}`),
		note("turn/completed", `{"turn":{"id":"turn-1","status":"completed"}}`),
		note("turn/completed", `{"threadId":"","turn":{"id":"turn-1","status":"completed"}}`),
		sleep(300),
		delta("m1", "after"),
		endTurn("completed"),
	})
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	if !strings.Contains(joinText(h.updates(), "agent_message_chunk"), "after") {
		t.Error("the prompt ended on an end that was not clearly the turn's")
	}
}

func TestNoCardAfterACancel(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		expect(`"method":"turn/interrupt"`),
		cmdApproval("ap-1", "c1", "make", ""),
		answered("ap-1", `{"decision":"cancel"}`),
		endTurn("interrupted"),
	})
	sid := h.start()
	p := h.promptText(sid, "go")
	waitLog(t, h.log, `"method":"turn/start"`)
	h.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": sid}})
	if got := stopReason(t, h.await(p)); got != "cancelled" {
		t.Errorf("stop = %s", got)
	}
	if n := len(h.requests("session/request_permission")); n != 0 {
		t.Errorf("%d cards after the cancel", n)
	}
	if logHas(readLog(h.log), "MISSING") {
		t.Error("the late request was not refused with cancel")
	}
}

func TestNoCardWhenNoPromptIsRunning(t *testing.T) {
	h := newHarness(t, []step{
		emit(`{"id":"ap-1","method":"item/commandExecution/requestApproval","params":{"threadId":"thr-1","turnId":"t","itemId":"c1","command":"id"}}`),
		answered("ap-1", `{"decision":"cancel"}`),
		sleep(300),
	})
	h.start()
	waitLog(t, h.log, `"id":"ap-1"`)
	if n := len(h.requests("session/request_permission")); n != 0 {
		t.Errorf("%d cards for a request outside a turn", n)
	}
}

func TestSecretsAreKeptOutOfTheLog(t *testing.T) {
	const tok = "tok-9f8e7d6c5b"
	steps := []step{expect(`"method":"turn/start"`), startedTurn(),
		note("error", `{"threadId":"thr-1","error":{"message":"bad header Bearer `+tok+` and env `+tok+`2"}}`),
		note("turn/completed", `{"threadId":"thr-1","turn":{"id":"turn-1","status":"failed","error":{"message":"failed with `+tok+`","codexErrorInfo":"other"}}}`)}
	h := newHarness(t, steps, hopts{extraEnv: []string{envStderr + "=codex says " + tok}})
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{
		map[string]any{"type": "http", "name": "web", "url": "https://example.test/mcp", "headers": []any{map[string]any{"name": "Authorization", "value": "Bearer " + tok}}},
		map[string]any{"name": "tools", "command": "x", "env": []any{map[string]any{"name": "T", "value": tok + "2"}}},
	}})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	sid := "thr-1"
	if r := h.await(h.promptText(sid, "go")); r.Error == nil {
		t.Fatal("the turn should have failed")
	}
	h.shutdown()
	logs := h.errlog.String()
	if !strings.Contains(logs, "[redacted]") {
		t.Errorf("nothing was logged, or nothing redacted: %q", logs)
	}
	if strings.Contains(logs, tok) {
		t.Errorf("a secret reached the log: %q", logs)
	}
}

func TestCompactThatStartsNoTurnEnds(t *testing.T) {
	h := newHarness(t, []step{expect(`"method":"thread/compact/start"`), sleep(60000)}, hopts{control: 600 * time.Millisecond})
	sid := h.start()
	start := time.Now()
	if got := stopReason(t, h.await(h.promptText(sid, "/compact"))); got != "end_turn" {
		t.Errorf("stop = %s", got)
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestImagesAreCheckedByContent(t *testing.T) {
	h := newHarness(t, []step{expect(`"method":"turn/start"`), startedTurn(), endTurn("completed")})
	sid := h.start()
	enc := func(b string) string { return base64.StdEncoding.EncodeToString([]byte(b)) }
	for name, c := range map[string]struct{ data, mime string }{
		"html as png":  {enc("<html><script>x</script></html>"), "image/png"},
		"png as jpeg":  {enc("\x89PNG\r\n\x1a\nxx"), "image/jpeg"},
		"short as gif": {enc("GIF"), "image/gif"},
	} {
		r := h.call("session/prompt", map[string]any{"sessionId": sid, "prompt": []any{
			map[string]any{"type": "image", "data": c.data, "mimeType": c.mime}}})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("%s: %+v", name, r)
		}
	}
	for _, c := range []struct{ data, mime string }{
		{enc("\xff\xd8\xffxx"), "image/jpeg"}, {enc("GIF89axx"), "image/gif"}, {enc("RIFF\x00\x00\x00\x00WEBPxx"), "image/webp"},
	} {
		if _, err := (&session{quit: make(chan struct{})}).stageImage(c.mime, c.data); err != nil {
			t.Errorf("%s: %v", c.mime, err)
		}
	}
	if logHas(readLog(h.log), `"method":"turn/start"`) {
		t.Error("a refused image reached codex")
	}
}

func TestAnImageBudgetPerSession(t *testing.T) {
	old := maxSessionImageBytes
	maxSessionImageBytes = 50
	t.Cleanup(func() { maxSessionImageBytes = old })
	h := newHarness(t, nil)
	sid := h.start()
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 12))) // 20 bytes
	img := map[string]any{"type": "image", "data": png, "mimeType": "image/png"}
	r := h.call("session/prompt", map[string]any{"sessionId": sid, "prompt": []any{img, img, img}})
	if r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Errorf("three images over a budget of two were accepted: %+v", r)
	}
	if logHas(readLog(h.log), `"method":"turn/start"`) {
		t.Error("a refused prompt reached codex")
	}
}
