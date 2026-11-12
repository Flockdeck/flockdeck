package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// Stage 0 of the remote parity plan: what a full device reached through the
// relay may and may not do on the control socket. Each test sends the command
// from a window dialled through the tunnel (dialRemoteControl), the way a
// phone's does, and looks at what the desk did.

// A prompt sent over the control socket is typed into the pane that has focus,
// or the one the window names, with Enter after it. Through the relay that
// socket is readable and forgeable by the relay (the terminal is a separate,
// sealed connection), so the command is the desk's, as paneSend is.
func TestARelayWindowCannotSendAPrompt(t *testing.T) {
	srv, ws := newTestServer(t)
	pane, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	sess := sessionOf(t, srv, ws, pane)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()

	// Named and unnamed: the focused pane is the default target.
	cmds := []command{
		{Cmd: "sendPrompt", Text: "echo stage0-focused"},
		{Cmd: "sendPrompt", ID: pane, Text: "echo stage0-named"},
	}
	for _, cmd := range cmds {
		sendCmd(t, phone, cmd)
	}
	time.Sleep(700 * time.Millisecond)
	for _, word := range []string{"stage0-focused", "stage0-named"} {
		if strings.Contains(sess.RecentText(8192), word) {
			t.Errorf("a prompt sent through the relay reached the pane: %q", word)
		}
	}
	for _, cmd := range cmds {
		var note noticeMsg
		readQuick(t, phone, "notice", &note)
		if !note.Error || !strings.Contains(note.Text, "machine") {
			t.Errorf("%s through the relay was answered %+v, want it refused", cmd.Cmd, note)
		}
	}

	// The desk's own window still can.
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "sendPrompt", ID: pane, Text: "echo stage0-desk"})
	waitForText(t, sess, "stage0-desk")
}

// A structured pane has no keys to type into, so sendPrompt never reaches one,
// from the desk or the relay. This is the first claim of the plan's finding F1
// ("can inject a prompt into a structured pane") and it is not true: the pane
// is skipped when the targets are chosen. The test holds that.
func TestSendPromptDoesNotReachAStructuredPane(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	if got, _ := ask(e.srv, func() int { return len(e.ws.BroadcastTargetsFor(e.child)) }); got != 0 {
		t.Fatalf("a structured pane is a target of sendPrompt: %d targets", got)
	}
	ts := remoteServer(t, e.srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "sendPrompt", ID: e.child, Text: "stage0-structured"})
	var note noticeMsg
	readQuick(t, phone, "notice", &note)
	if !note.Error {
		t.Errorf("sendPrompt at a structured pane through the relay was answered %+v, want a refusal", note)
	}
}

// Turning recording on for the first time is put to the user in the window, and
// the command arrives with confirmed set. A relay window can set that itself,
// which turns the acknowledgement on without anyone at the desk reading what is
// stored. Recording is the desk's.
func TestARelayWindowCannotStartARecording(t *testing.T) {
	srv, ws := newTestServer(t)
	pane, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	if store.LoadPrefs().RecordingAcknowledged {
		t.Fatal("recording is already acknowledged")
	}
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()

	sendCmd(t, phone, command{Cmd: "recordPane", ID: pane, Recording: true, Confirmed: true})
	var note noticeMsg
	readQuick(t, phone, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "machine") {
		t.Errorf("recordPane through the relay was answered %+v, want it refused", note)
	}
	if store.LoadPrefs().RecordingAcknowledged {
		t.Error("a window through the relay acknowledged recording for the desk")
	}
}

// relayRecorder is a relay that answers every call and keeps the ones that
// change a device.
type relayRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *relayRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		if req.Method != http.MethodGet {
			r.calls = append(r.calls, req.Method+" "+req.URL.Path)
		}
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
}

func (r *relayRecorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// reconnectCounter counts how many times the relay connection was restarted.
type reconnectCounter struct {
	*fakeRemote
	n atomic.Int32
}

func (r *reconnectCounter) Reconnect() error { r.n.Add(1); return nil }

// Unpairing or renaming a device, renaming this machine and restarting the
// relay connection are the dialog's, done at the desk: a phone that could
// unpair the others would let whoever holds one unlocked phone lock out the
// rest, and one that could restart the connection drops every window on it.
func TestARelayWindowCannotUnpairRenameOrReconnect(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := &relayRecorder{}
	relay := httptest.NewServer(rec.handler())
	defer relay.Close()
	fake := &reconnectCounter{fakeRemote: &fakeRemote{client: &remote.Client{Relay: relay.URL, Token: "fdh_test"}}}
	srv.SetRemote(fake)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()

	type outcome struct{ Type, Action, Error string }
	for _, cmd := range []command{
		{Cmd: "remoteRevoke", ID: "dev-2"},
		{Cmd: "remoteRename", Kind: "device", ID: "dev-2", Name: "stolen"},
		{Cmd: "remoteRename", Kind: "host", Name: "renamed-by-phone"},
		{Cmd: "remoteReconnect"},
	} {
		sendCmd(t, phone, cmd)
		if cmd.Cmd == "remoteReconnect" {
			// The dialog's button waits for an outcome, so a refusal is one.
			var out outcome
			readUntil(t, phone, "remoteOutcome", &out)
			if out.Error == "" {
				t.Errorf("%s through the relay was answered %+v, want it refused", cmd.Cmd, out)
			}
		}
		var note noticeMsg
		readUntil(t, phone, "notice", &note)
		if !note.Error || !strings.Contains(note.Text, "machine") {
			t.Errorf("%+v through the relay was told %+v, want to do it on the machine itself", cmd, note)
		}
	}
	if calls := rec.seen(); len(calls) != 0 {
		t.Errorf("a window through the relay changed devices at the relay: %v", calls)
	}
	if len(fake.renamed) != 0 {
		t.Errorf("a window through the relay renamed the machine: %v", fake.renamed)
	}
	if n := fake.n.Load(); n != 0 {
		t.Errorf("a window through the relay restarted the relay connection %d times", n)
	}

	// The desk still can, and so does a window on it.
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "remoteRevoke", ID: "dev-2"})
	sendCmd(t, desk, command{Cmd: "remoteRename", Kind: "device", ID: "dev-2", Name: "kitchen"})
	sendCmd(t, desk, command{Cmd: "remoteReconnect"})
	deadline := time.Now().Add(10 * time.Second)
	for (len(rec.seen()) < 2 || fake.n.Load() < 1) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := rec.seen(); len(got) != 2 || fake.n.Load() != 1 {
		t.Errorf("the desk's own window: relay calls %v, reconnects %d, want a revoke, a rename and one reconnect", got, fake.n.Load())
	}
}

// The folder picker lists any folder on the machine. A window reached through
// the relay is let list the folders of a project Flockdeck knows (open or
// recent) and no others, so it cannot read the layout of the rest of the disk.
func TestARelayWindowBrowsesOnlyKnownProjects(t *testing.T) {
	srv, ws := newTestServer(t)
	known := ws.ActiveRoot()
	if err := os.MkdirAll(filepath.Join(known, "inside-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "secret-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()

	names := func(m browseMsg) []string {
		var out []string
		for _, e := range m.Entries {
			out = append(out, e.Name)
		}
		return out
	}
	for _, path := range []string{outside, filepath.Dir(outside), ""} {
		sendCmd(t, phone, command{Cmd: "browse", Path: path, Req: "b"})
		var m browseMsg
		readUntil(t, phone, "browse", &m)
		if m.Error == "" || len(m.Entries) != 0 || len(m.Places) != 0 {
			t.Errorf("browse %q through the relay was answered with %d folders, %d places and error %q (%v), want a refusal", path, len(m.Entries), len(m.Places), m.Error, names(m))
		}
		if m.Req != "b" {
			t.Errorf("browse %q: the refusal does not carry the req: %+v", path, m)
		}
	}

	// A folder inside a known project is listed.
	sendCmd(t, phone, command{Cmd: "browse", Path: known, Req: "k"})
	var m browseMsg
	readUntil(t, phone, "browse", &m)
	if m.Error != "" || !containsString(names(m), "inside-folder") {
		t.Errorf("browse of a known project through the relay was answered %+v", m)
	}

	// The window on this machine lists anything, as it always has.
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "browse", Path: outside, Req: "d"})
	var dm browseMsg
	readUntil(t, desk, "browse", &dm)
	if dm.Error != "" || !containsString(names(dm), "secret-folder") {
		t.Errorf("the desk's own browse was answered %+v", dm)
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// startAgent from a relay window is limited to an open project already: a
// root that is not one is refused. This is the second half of the plan's
// finding F4 and it is not a gap, so nothing was changed; the test holds it.
func TestARelayWindowCannotStartAnAgentOutsideAnOpenProject(t *testing.T) {
	srv, _ := newTestServer(t)
	outside := t.TempDir()
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "startAgent", Root: outside, Task: "stage0", Req: "s"})
	var note noticeMsg
	readQuick(t, phone, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "not an open project") {
		t.Errorf("startAgent at a folder that is not an open project was answered %+v", note)
	}
}
