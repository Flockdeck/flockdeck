package server

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// Stage 9: two gaps the earlier stages left. Neither widens anything; both hold
// what the docs (docs/remote.md) say.

// Whatever tier A message a device is sent, a secret in any of its strings is
// redacted and every field the table strips is gone, for every type in the table
// and not only the ones each stage's own test names.
func TestSealedTierAFramesCarryNoSecretAndNoStrippedField(t *testing.T) {
	yes := func() bool { return true }
	var types []string
	for typ, rule := range sealedMessages {
		if rule.Tier == sealedTierA {
			types = append(types, typ)
		}
	}
	sort.Strings(types)
	if len(types) == 0 {
		t.Fatal("no tier A message types")
	}
	for _, typ := range types {
		rule := sealedMessages[typ]
		in := map[string]any{"type": typ, "text": sealedSecret, "title": "k " + sealedSecret, "id": "p"}
		for _, k := range rule.strip {
			in[k] = "stripped-value"
		}
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		// A scrub may turn the message into nothing (a refusal that holds data is
		// dropped), which is a pass; what is sent must be clean.
		for _, act := range []func() bool{nil, yes} {
			out := sealedWrite(raw, act)
			if out == nil {
				continue
			}
			if bytes.Contains(out, []byte(sealedSecret)) {
				t.Errorf("%s carried a secret: %s", typ, out)
			}
			for _, k := range rule.strip {
				if bytes.Contains(out, []byte(`"`+k+`"`)) || bytes.Contains(out, []byte("stripped-value")) {
					t.Errorf("%s carried the stripped field %s: %s", typ, k, out)
				}
			}
		}
	}
}

// Every tier B message is dropped for a device that may not act, whatever the
// message holds.
func TestSealedTierBFramesNeedTheRightEveryTime(t *testing.T) {
	no := func() bool { return false }
	for typ, rule := range sealedMessages {
		if rule.Tier != sealedTierB {
			continue
		}
		raw := []byte(`{"type":"` + typ + `","id":"p","req":"r","ok":true}`)
		if got := sealedWrite(raw, no); got != nil {
			t.Errorf("%s was written to a device that may not act: %s", typ, got)
		}
		if got := sealedWrite(raw, nil); got != nil {
			t.Errorf("%s was written with no check: %s", typ, got)
		}
	}
}

// A role change that did not cut the socket still takes effect on the next
// request: a device that was full and is now a viewer is refused the commands a
// full device alone may run, and the tier B ones, by code.
func TestSealedRegatesEachRequestOnARoleChange(t *testing.T) {
	e := newSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")

	// A full device runs a full-only read (the answer is the handler's, not a refusal).
	p.send(command{Cmd: "recents", Req: "a"})
	p.readType("recents")

	// The desk makes it a viewer that may watch panes. Nothing cuts the socket here.
	putAccess(t, e.srv, "full", store.DeviceAccess{Role: store.RoleViewer, WatchPanes: true})
	e.srv.approveFile = approveFileCache{}

	p.send(command{Cmd: "recents", Req: "b"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeRole {
		t.Errorf("a full-only read after the role change: %v", got)
	}
	p.send(command{Cmd: "paneSend", ID: "nope", Text: "hi", Req: "c"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeControl {
		t.Errorf("a tier B request after the role change: %v", got)
	}
	// What a viewer may read is still served on the same socket.
	p.send(command{Cmd: "conversationClose", ID: "x"})
}
