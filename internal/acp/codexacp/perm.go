package codexacp

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"sync"
)

const (
	optAllow       = "allow"
	optAllowAlways = "allow_always"
	optReject      = "reject"
)

// Approval policy. Every request from codex that asks to do something is
// refused unless the client answers with an option this adapter offered, and a
// request the adapter does not understand is refused with an error. A
// request that reaches a limit, names another thread, or finds the client
// gone is refused too.
//
// "Allow always" is offered for a shell command only, and is kept by the
// adapter, in memory, for the life of the session: the rule is the command with
// everything codex sent about it, so it covers that call and no other. It is
// never sent to codex as acceptForSession or as an amendment, whose reach is
// codex's to decide. A file change and an extra permission are asked about each
// time: a rule for either would cover paths and grants nobody saw.

// canAlwaysKeys are the members of a command approval request that a rule may
// be made across. A request with any other member set (a network context, extra
// permissions, anything new in a later codex) is asked about each time.
var canAlwaysKeys = map[string]bool{
	"threadId": true, "turnId": true, "itemId": true, "reason": true, "command": true,
	"cwd": true, "commandActions": true, "kind": true, "approvalId": true,
	"proposedExecpolicyAmendment": true, "proposedNetworkPolicyAmendments": true, "availableDecisions": true,
}

// maxShownPermissions is how much of an extra-permissions request the card shows.
// A request longer than that is refused, not granted unseen.
const maxShownPermissions = 2000

// openPerm is a request from codex waiting for an answer. Whoever answers
// first wins: the client's answer, a cancel, or nothing if codex withdraws it.
type openPerm struct {
	raw           json.RawMessage // codex's id for the request
	cancelPayload map[string]any
	abandoned     chan struct{}
	aonce         sync.Once
	once          sync.Once
}

func (p *openPerm) abandon() { p.aonce.Do(func() { close(p.abandoned) }) }

// answer sends codex the result of the request. Only the first call does.
func (p *openPerm) answer(ss *session, payload map[string]any) {
	p.once.Do(func() { _ = ss.write(map[string]any{"id": p.raw, "result": payload}) })
}

// approval is one request, ready to be put to the client.
type approval struct {
	threadID string
	itemID   string
	call     toolDesc
	allow    map[string]any
	deny     map[string]any
	cancel   map[string]any
	always   string // the rule key, "" when the card has no always option
	alwaysOn string // the name of the always option
}

// networkTitle starts the title of an approval to reach the network.
const networkTitle = "Network access: "

func decision(d string) map[string]any { return map[string]any{"decision": d} }

func (ss *session) handleRequest(m rpcMsg) {
	var p struct {
		ThreadID    string          `json:"threadId"`
		ItemID      string          `json:"itemId"`
		Reason      string          `json:"reason"`
		Command     string          `json:"command"`
		Cwd         string          `json:"cwd"`
		Kind        string          `json:"kind"`
		GrantRoot   string          `json:"grantRoot"`
		Permissions json.RawMessage `json:"permissions"`
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(m.Params, &p) != nil || json.Unmarshal(m.Params, &members) != nil {
		ss.refuse(m.ID, "bad parameters")
		return
	}
	switch m.Method {
	case "item/commandExecution/requestApproval":
		d := ss.describe(&item{Type: "commandExecution", Command: p.Command, Cwd: p.Cwd})
		if netCtx := members["networkApprovalContext"]; !isNull(netCtx) {
			d.title = networkTitle + d.title
			p.Reason = strings.TrimSpace(p.Reason + "\n" + capText(string(netCtx), 500))
		}
		a := approval{threadID: p.ThreadID, itemID: p.ItemID, call: d,
			allow: decision("accept"), deny: decision("decline"), cancel: decision("cancel")}
		if p.Kind == "" || p.Kind == "command" {
			a.always, a.alwaysOn = alwaysKey(members, p.Command, p.Cwd), "Always allow this command"
		}
		if p.Reason != "" {
			a.call.content = textBlock(p.Reason)
		}
		ss.ask(m, a)
	case "item/fileChange/requestApproval":
		d := toolDesc{kind: "edit", title: "Edit files"}
		reason := p.Reason
		if p.GrantRoot != "" {
			reason = strings.TrimSpace(reason + "\nCodex also asks to write under " + p.GrantRoot)
		}
		if reason != "" {
			d.content = textBlock(reason)
		}
		ss.ask(m, approval{threadID: p.ThreadID, itemID: p.ItemID, call: d,
			allow: decision("accept"), deny: decision("decline"), cancel: decision("cancel")})
	case "item/permissions/requestApproval":
		none := map[string]any{"permissions": map[string]any{}, "scope": "turn"}
		d := toolDesc{kind: "other", title: "Extra permissions",
			content: textBlock(strings.TrimSpace(p.Reason + "\n" + capText(string(p.Permissions), maxShownPermissions)))}
		a := approval{threadID: p.ThreadID, itemID: p.ItemID, call: d, deny: none, cancel: none}
		if isNull(p.Permissions) || len(p.Permissions) > maxShownPermissions {
			// Nothing is named, or more than the card shows: there is nothing
			// the person could have agreed to, so nothing is granted.
			a.allow = none
		} else {
			a.allow = map[string]any{"permissions": p.Permissions, "scope": "turn"}
		}
		ss.ask(m, a)
	default:
		ss.refuse(m.ID, "not supported by this client")
	}
}

func isNull(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}

// alwaysKey is the rule an "allow always" answer is remembered by, or "" when
// this request may not have one.
func alwaysKey(members map[string]json.RawMessage, command, cwd string) string {
	if command == "" {
		return ""
	}
	for k, v := range members {
		if !canAlwaysKeys[k] && !isNull(v) {
			return ""
		}
	}
	b, err := json.Marshal(map[string]string{"command": command, "cwd": cwd})
	if err != nil {
		return ""
	}
	return "command\x00" + string(bytes.TrimSpace(b))
}

// ask puts an approval request to the client and answers codex with what it
// says. It runs on its own goroutine.
func (ss *session) ask(m rpcMsg, a approval) {
	op := &openPerm{raw: *m.ID, cancelPayload: a.cancel, abandoned: make(chan struct{})}
	if a.threadID != ss.tid() || a.itemID == "" || closedCh(ss.quit) {
		op.answer(ss, a.deny)
		return
	}
	key := string(*m.ID)
	ss.mu.Lock()
	// A card appears only for the turn that is running: none after a cancel,
	// and none when no prompt of ours is waiting.
	if t := ss.cur; t == nil || t.cancelled || t.gen != ss.gen {
		ss.mu.Unlock()
		op.answer(ss, a.cancel)
		return
	}
	if st := ss.items[a.itemID]; st != nil && st.desc != nil && st.desc.truncated {
		// The card could not show all of it.
		ss.mu.Unlock()
		op.answer(ss, a.deny)
		return
	}
	if len(ss.perms) >= maxPerms || ss.perms[key] != nil {
		ss.mu.Unlock()
		op.answer(ss, a.deny)
		return
	}
	if a.always != "" && ss.allowed[a.always] {
		ss.mu.Unlock()
		op.answer(ss, a.allow)
		return
	}
	ss.perms[key] = op
	st := ss.state(a.itemID)
	announced := st != nil && st.announced
	if st != nil {
		st.announced = true
		if st.desc != nil {
			// The item's own card has the diff or the command. The request's reason
			// (why Codex asks, a network host, a root it also wants to write under)
			// is kept after it: an approval of network access must not look like
			// the plain command.
			reason, network := a.call.content, strings.HasPrefix(a.call.title, networkTitle)
			a.call.content, a.call.locations, a.call.rawInput = slices.Concat(st.desc.content, reason), st.desc.locations, st.desc.rawInput
			a.call.title, a.call.kind = st.desc.title, st.desc.kind
			if network {
				a.call.title = networkTitle + a.call.title
			}
		} else {
			st.desc = &a.call
		}
	}
	ss.mu.Unlock()
	defer func() {
		ss.mu.Lock()
		delete(ss.perms, key)
		ss.mu.Unlock()
	}()

	if !announced {
		ss.emit(ss.toolCall(a.itemID, a.call, "pending"))
	}
	call := map[string]any{"toolCallId": a.itemID, "title": a.call.title, "kind": a.call.kind, "status": "pending"}
	if a.call.rawInput != nil {
		call["rawInput"] = a.call.rawInput
	}
	if len(a.call.locations) > 0 {
		call["locations"] = a.call.locations
	}
	if len(a.call.content) > 0 {
		call["content"] = a.call.content
	}
	options := []any{map[string]any{"optionId": optAllow, "name": "Allow", "kind": "allow_once"}}
	if a.always != "" {
		options = append(options, map[string]any{"optionId": optAllowAlways, "name": a.alwaysOn, "kind": "allow_always"})
	}
	options = append(options, map[string]any{"optionId": optReject, "name": "Reject", "kind": "reject_once"})
	id, ch := ss.srv.request("session/request_permission", map[string]any{
		"sessionId": ss.tid(),
		"toolCall":  call,
		"options":   options,
	})
	select {
	case r := <-ch:
		// An answer that raced a cancel or a withdrawal is dropped: both
		// channels can be ready at once, and the cancel has already answered.
		select {
		case <-op.abandoned:
			return
		default:
		}
		var out struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(r.Result, &out)
		switch {
		case r.Error != nil || out.Outcome.Outcome != "selected":
			op.answer(ss, a.cancel)
		case out.Outcome.OptionID == optAllow:
			ss.grant(op, a, "")
		case out.Outcome.OptionID == optAllowAlways && a.always != "":
			ss.grant(op, a, a.always)
		default: // reject, or an option that was not offered
			op.answer(ss, a.deny)
		}
	case <-op.abandoned:
		// Codex withdrew the request or the turn was cancelled: the card is
		// gone and nothing is sent (a cancel has refused it already).
		ss.srv.forget(id)
	case <-ss.dead:
		ss.srv.forget(id)
	case <-ss.quit:
		ss.srv.forget(id)
	}
}

// grant allows the request, remembering rule if there is one.
func (ss *session) grant(op *openPerm, a approval, rule string) {
	if rule != "" {
		ss.mu.Lock()
		ss.allowed[rule] = true
		ss.mu.Unlock()
	}
	op.answer(ss, a.allow)
	ss.emit(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": a.itemID, "status": "in_progress"})
}
