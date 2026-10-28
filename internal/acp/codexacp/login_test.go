package codexacp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/agentlogin"
)

func loginHarness(t *testing.T, mode string) (*harness, string) {
	t.Helper()
	beat := filepath.Join(t.TempDir(), "beat")
	return newHarness(t, nil, hopts{extraEnv: []string{envLogin + "=" + mode, envBeat + "=" + beat}}), beat
}

func urlNotice(m rpcMsg) bool { return m.Method == agentlogin.URLNotification }

func TestInitializeListsTheLoginMethod(t *testing.T) {
	h, _ := loginHarness(t, "ok")
	r := h.call("initialize", map[string]any{"protocolVersion": 1})
	var init struct {
		AuthMethods []struct{ ID, Name string } `json:"authMethods"`
	}
	_ = json.Unmarshal(r.Result, &init)
	if len(init.AuthMethods) != 1 || init.AuthMethods[0].ID != agentlogin.MethodID || init.AuthMethods[0].Name != agentlogin.MethodName {
		t.Errorf("authMethods = %s", r.Result)
	}
}

func TestAuthenticateRunsCodexLoginAndSendsTheURL(t *testing.T) {
	h, _ := loginHarness(t, "ok")
	id := h.send("authenticate", map[string]any{"methodId": agentlogin.MethodID})
	n := h.waitFor(urlNotice, 20*time.Second)
	var p struct{ URL string }
	_ = json.Unmarshal(n.Params, &p)
	if !strings.HasPrefix(p.URL, "https://auth.openai.com/oauth/authorize?") {
		t.Errorf("url = %q", p.URL)
	}
	if r := h.await(id); r.Error != nil {
		t.Fatalf("authenticate failed: %+v", r.Error)
	}
	log, _ := os.ReadFile(h.log)
	if !strings.Contains(string(log), "LOGIN_CALL login") {
		t.Errorf("codex was not run as `auth login`: %q", log)
	}
}

func TestAuthenticateFailureSaysOnlyFixedText(t *testing.T) {
	h, _ := loginHarness(t, "fail")
	r := h.call("authenticate", map[string]any{"methodId": agentlogin.MethodID})
	if r.Error == nil || r.Error.Message != "the sign-in did not finish" {
		t.Fatalf("error = %+v", r.Error)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "SECRET") {
		t.Errorf("the login command's output reached the reply: %s", raw)
	}
}

func TestAuthenticateRefusesOtherMethods(t *testing.T) {
	h, _ := loginHarness(t, "ok")
	r := h.call("authenticate", map[string]any{"methodId": "api-key"})
	if r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Fatalf("error = %+v", r.Error)
	}
	if log, _ := os.ReadFile(h.log); strings.Contains(string(log), "LOGIN_CALL") {
		t.Error("codex was run for a method the adapter does not offer")
	}
}

func TestAuthenticateDoesNotForwardAddressesThatFailValidation(t *testing.T) {
	h, _ := loginHarness(t, "evil")
	r := h.call("authenticate", map[string]any{"methodId": agentlogin.MethodID})
	if r.Error != nil {
		t.Fatalf("error = %+v", r.Error)
	}
	for _, m := range h.snapshotMsgs() {
		if urlNotice(m) {
			t.Errorf("an address was forwarded: %s", m.Params)
		}
	}
}

func TestOnlyOneSignInAtATime(t *testing.T) {
	h, _ := loginHarness(t, "hang")
	first := h.send("authenticate", map[string]any{"methodId": agentlogin.MethodID})
	h.waitFor(urlNotice, 20*time.Second)
	r := h.call("authenticate", map[string]any{"methodId": agentlogin.MethodID})
	if r.Error == nil || r.Error.Message != "a sign-in is already running" {
		t.Fatalf("second error = %+v", r.Error)
	}
	_ = first
}

func TestClosingTheAdapterEndsTheLoginCommand(t *testing.T) {
	h, beat := loginHarness(t, "hang")
	h.send("authenticate", map[string]any{"methodId": agentlogin.MethodID})
	h.waitFor(urlNotice, 20*time.Second)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if fi, err := os.Stat(beat); err == nil && fi.Size() > 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the login command never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.in.Close()
	select {
	case <-h.stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the adapter did not stop")
	}
	time.Sleep(300 * time.Millisecond)
	a, _ := os.Stat(beat)
	time.Sleep(400 * time.Millisecond)
	b, _ := os.Stat(beat)
	if a.Size() != b.Size() {
		t.Fatalf("the login command is still running (%d -> %d)", a.Size(), b.Size())
	}
}

// The hosts are the claim in docs/acp-codex-adapter.md; widening the list is a
// decision, not a side effect.
func TestLoginHostsAreCodexOwn(t *testing.T) {
	if got := strings.Join(loginHosts, ","); got != "openai.com,chatgpt.com" {
		t.Errorf("loginHosts = %s", got)
	}
}
