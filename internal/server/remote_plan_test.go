package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmwri/flockdeck/internal/remote"
)

// relayedRemote is remote access with a relay to ask.
type relayedRemote struct {
	fakeRemote
	client *remote.Client
}

func (r *relayedRemote) Client() (*remote.Client, error) { return r.client, nil }

// The account's plan, when the relay reports one, reaches the window with
// the roster, as it came: the window shows it, and nothing here acts on it.
func TestTheRosterCarriesTheRelaysPlan(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"devices":[],"hosts":[],"plan":{"plan":"trial","name":"Free trial","active":true,"ends":"2030-01-31T00:00:00Z"}}`)
	}))
	defer relay.Close()
	srv, _ := newTestServer(t)
	srv.SetRemote(&relayedRemote{client: remote.NewClient(&remote.Config{Relay: relay.URL, Token: "fdh_test"}, "v")})
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "remoteDevices"})
	var msg struct {
		Enabled bool
		Plan    *remote.Plan
	}
	readUntil(t, conn, "remoteDevices", &msg)
	if !msg.Enabled || msg.Plan == nil || msg.Plan.Plan != "trial" || msg.Plan.Name != "Free trial" || !msg.Plan.Active {
		t.Errorf("the window was sent %+v; want the relay's plan", msg)
	}
}
