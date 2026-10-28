package codexacp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jmwri/flockdeck/internal/acp/agentlogin"
)

// loginHosts are the hosts codex's sign-in page can be on: auth.openai.com for
// the ChatGPT sign-in, and chatgpt.com.
var loginHosts = []string{"openai.com", "chatgpt.com"}

// authMethods is what initialize lists: the one way the adapter signs codex
// in. Fixed text; codex's own words are never used.
func authMethods() []any {
	return []any{map[string]any{
		"id":          agentlogin.MethodID,
		"name":        agentlogin.MethodName,
		"description": "Runs codex's login command and gives the sign-in address.",
	}}
}

// authenticate answers the ACP authenticate request. The only method is the
// login command: `codex login`, run as a process of its own. The address
// it prints goes to the client in a notification, after validation; the reply
// comes when the command has ended. Whatever the command prints is not passed
// on or logged.
func (s *server) authenticate(m rpcMsg) {
	var p struct {
		MethodID string `json:"methodId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	if p.MethodID != agentlogin.MethodID {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "no such authentication method"})
		return
	}
	err := s.login.Do(context.Background(), agentlogin.Spec{
		Path:  s.opts.CodexPath,
		Args:  []string{"login"},
		Env:   s.opts.Env,
		Hosts: loginHosts,
	}, func(u string) {
		s.send(map[string]any{"jsonrpc": "2.0", "method": agentlogin.URLNotification, "params": map[string]any{"url": u}})
	})
	switch {
	case err == nil:
		s.reply(m.ID, map[string]any{})
	case errors.Is(err, agentlogin.ErrBusy):
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: "a sign-in is already running"})
	default:
		s.logf("sign-in: %v", err)
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: "the sign-in did not finish"})
	}
}
