package server

import (
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/ui/help"
)

// applyDeltas is what a window does with keys.json: the defaults, the
// remappings laid over them and the hidden ids taken out.
func applyDeltas(overrides map[string]string, hidden []string) []keyView {
	var out []keyView
	for _, k := range help.Keys {
		if slices.Contains(hidden, k.ID) {
			continue
		}
		v := keyView{}
		v.ID, v.Keys, v.Label, v.Short, v.Section, v.Page = k.ID, k.Keys, k.Label, k.Short, k.Section, k.Page
		v.NoPalette, v.Confirm = k.NoPalette, k.Confirm
		if o, ok := overrides[k.ID]; ok {
			v.Keys, v.Overridden = o, true
		}
		out = append(out, v)
	}
	return out
}

// keys.json plus the overrides in the hello is the table the hello carries
// whole, for the desk's window and for one through the relay, and a remap
// reaches both through the keyTable push.
func TestKeysJSONPlusOverridesIsTheTableTheHelloCarries(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	var hello helloMsg
	readUntil(t, conn, "hello", &hello)
	if !slices.Contains(hello.API.Caps, "keyOverrides") {
		t.Fatalf("caps %v lack keyOverrides", hello.API.Caps)
	}
	if len(hello.KeyOverrides) != 0 || len(hello.KeysHidden) != 0 {
		t.Fatalf("a fresh desk window got overrides %v, hidden %v", hello.KeyOverrides, hello.KeysHidden)
	}
	if got := applyDeltas(hello.KeyOverrides, hello.KeysHidden); !slices.EqualFunc(got, hello.Keys, func(a, b keyView) bool { return a == b }) {
		t.Fatal("keys.json alone differs from the table in the hello")
	}

	sendCmd(t, conn, command{Cmd: "setKeybinding", ID: "closePane", Text: "Ctrl+Alt+W"})
	var push keyTableMsg
	readUntil(t, conn, "keyTable", &push)
	if push.KeyOverrides["closePane"] != "Ctrl+Alt+W" || len(push.KeyOverrides) != 1 {
		t.Fatalf("keyTable overrides = %v, want just closePane", push.KeyOverrides)
	}
	if got := applyDeltas(push.KeyOverrides, push.KeysHidden); !slices.EqualFunc(got, push.Keys, func(a, b keyView) bool { return a == b }) {
		t.Fatal("keys.json plus the overrides differs from the keyTable push")
	}

	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	var remote helloMsg
	readUntil(t, phone, "hello", &remote)
	for _, id := range []string{"detach", "quit", "helpers"} {
		if !slices.Contains(remote.KeysHidden, id) {
			t.Errorf("a relay window was not told to hide %s: %v", id, remote.KeysHidden)
		}
	}
	if got := applyDeltas(remote.KeyOverrides, remote.KeysHidden); !slices.EqualFunc(got, remote.Keys, func(a, b keyView) bool { return a == b }) {
		t.Fatal("keys.json plus the overrides differs from the table a relay window is sent")
	}
}
