package server

import "testing"

// TestARemoteWindowIsNotOfferedQuitOrDetach covers the palette on a phone. A
// window reached through the relay is refused Detach and Quit, which are the
// desk's to choose, and was offered both anyway -- Quit asking whether to stop
// every agent in every project only for the answer to be no.
func TestARemoteWindowIsNotOfferedQuitOrDetach(t *testing.T) {
	srv, _ := newTestServer(t)
	offered := func(msg helloMsg) map[string]bool {
		ids := map[string]bool{}
		for _, k := range msg.Keys {
			ids[k.ID] = true
		}
		return ids
	}

	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	var remote helloMsg
	readUntil(t, phone, "hello", &remote)
	got := offered(remote)
	if got["quit"] || got["detach"] {
		t.Errorf("a window through the relay was offered quit=%v detach=%v; want neither", got["quit"], got["detach"])
	}
	if !got["help"] {
		t.Error("the relayed window's table lost more than Quit and Detach")
	}

	var local helloMsg
	readUntil(t, dialControl(t, srv), "hello", &local)
	if got := offered(local); !got["quit"] || !got["detach"] {
		t.Errorf("a window on this machine was offered quit=%v detach=%v; want both", got["quit"], got["detach"])
	}
}
