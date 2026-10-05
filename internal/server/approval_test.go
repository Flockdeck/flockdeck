package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/hooks"
)

// dialWindow is dialControl as a browser does it: with an Origin header.
func dialWindow(t *testing.T, srv *Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws/control?t="+srv.Token(),
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://" + srv.Addr()}}})
	if err != nil {
		t.Fatalf("dial window: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// nextApproval reads until a notice that asks for approval arrives.
func nextApproval(t *testing.T, conn *websocket.Conn) approvalNotice {
	t.Helper()
	var n approvalNotice
	for n.Action == nil {
		readUntil(t, conn, "notice", &n)
	}
	return n
}

// noApproval reports whether no notice asking for approval reaches conn within d.
func noApproval(conn *websocket.Conn, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		ctx, cancel := context.WithDeadline(context.Background(), end)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			return true
		}
		var n approvalNotice
		if json.Unmarshal(data, &n) == nil && n.Type == "notice" && n.Action != nil {
			return false
		}
	}
	return true
}

func writeNotesIn() string {
	dir, _ := os.MkdirTemp("", "notes")
	path := filepath.Join(dir, "notes.md")
	_ = os.WriteFile(path, []byte("finish the thing"), 0o600)
	return path
}

func spawnElsewhere(hook *hooks.Server, pane string) chan error {
	result := make(chan error, 1)
	notes := writeNotesIn()
	go func() {
		_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), pane,
			hooks.SpawnRequest{Task: "x", Agent: "gitcli", Baton: "path:" + notes, BatonElsewhere: true})
		result <- err
	}()
	return result
}

func waitingStill(t *testing.T, result chan error, what string, d time.Duration) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("%s: the spawn went ahead or failed: %v", what, err)
	case <-time.After(d):
	}
}

func shortApprovalWait(t *testing.T, d time.Duration) {
	t.Helper()
	old := batonApprovalWait
	batonApprovalWait = d
	t.Cleanup(func() { batonApprovalWait = old })
}

// An agent that passes -baton-send-elsewhere has only asked: the baton is sent
// when the user allows it in the window, with a token only that window was given.
func TestSendingABatonElsewhereNeedsTheUserToAllowItInTheWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialWindow(t, srv)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 20*time.Second)

	result := spawnElsewhere(hook, pane)
	n := nextApproval(t, conn)
	if n.Action.Send["cmd"] != "approveBaton" || n.Action.Send["text"] == "" || n.LifeMS != 20000 || n.Lead == "" || !n.Pin || n.ID == "" {
		t.Fatalf("the notice does not ask for approval: %+v", n)
	}
	// It says where the baton would go and where it came from.
	for _, want := range []string{"Git as an agent", "(git)", "unknown source"} {
		if !strings.Contains(n.Text, want) {
			t.Errorf("the notice does not say %q: %s", want, n.Text)
		}
	}
	waitingStill(t, result, "before anyone allowed it", 200*time.Millisecond)

	// A token that was not given out does nothing.
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: "not-a-token"})
	var bad approvalNotice
	readUntil(t, conn, "notice", &bad)
	if !bad.Error || !strings.Contains(bad.Text, "run out") {
		t.Errorf("a made-up token: %+v", bad)
	}
	waitingStill(t, result, "after a made-up token", 100*time.Millisecond)

	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	select {
	case err := <-result:
		if err != nil && (strings.Contains(err.Error(), "not allowed") || strings.Contains(err.Error(), "different company")) {
			t.Errorf("allowed in the window and still refused: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the spawn did not go ahead once it was allowed")
	}
	// The notice is withdrawn, and the token is used once.
	var gone struct {
		ID string `json:"id"`
	}
	readUntil(t, conn, "noticeWithdraw", &gone)
	if gone.ID != n.ID {
		t.Errorf("withdrew %q, want %q", gone.ID, n.ID)
	}
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	var again approvalNotice
	for !again.Error {
		readUntil(t, conn, "notice", &again)
	}
	if !strings.Contains(again.Text, "run out") {
		t.Errorf("a used token was accepted again: %+v", again)
	}
}

// A connection that did not open with an Origin never gets the notice, and cannot
// approve with a token it learned some other way.
func TestAConnectionWithoutAnOriginIsNeverAskedAndCannotApprove(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	window := dialWindow(t, srv)
	bare := dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 20*time.Second)

	result := spawnElsewhere(hook, pane)
	n := nextApproval(t, window)
	if !noApproval(bare, 500*time.Millisecond) {
		t.Fatal("a connection with no Origin was sent the approval notice")
	}
	// It has the token anyway, say from the window's own page, and is refused. (Reading
	// with a deadline closes the first connection, so another is made.)
	bare = dialControl(t, srv)
	sendCmd(t, bare, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	var refusal approvalNotice
	for !refusal.Error {
		readUntil(t, bare, "notice", &refusal)
	}
	waitingStill(t, result, "after a connection with no Origin tried", 200*time.Millisecond)
	sendCmd(t, window, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	select {
	case <-result:
	case <-time.After(30 * time.Second):
		t.Fatal("the window's own approval did not go through")
	}
}

func TestOnlyAConnectionWithoutAnOriginIsNotAskedAtAll(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	_ = dialControl(t, srv)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	err := <-spawnElsewhere(hook, pane)
	if err == nil || !strings.Contains(err.Error(), "no Flockdeck window") {
		t.Errorf("err = %v, want a refusal that says no window is open", err)
	}
}

// With two windows each is given a token of its own; a token does nothing from
// the other, and once one window allows it the other is told so.
func TestAWindowCannotUseAnotherWindowsToken(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	one, two := dialWindow(t, srv), dialWindow(t, srv)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 20*time.Second)

	result := spawnElsewhere(hook, pane)
	a, b := nextApproval(t, one), nextApproval(t, two)
	if a.Action.Send["text"] == b.Action.Send["text"] {
		t.Fatal("both windows were given the same token")
	}
	sendCmd(t, two, command{Cmd: "approveBaton", Text: a.Action.Send["text"]})
	var refusal approvalNotice
	for !refusal.Error {
		readUntil(t, two, "notice", &refusal)
	}
	if !strings.Contains(refusal.Text, "another window") {
		t.Errorf("another window's token: %+v", refusal)
	}
	waitingStill(t, result, "after the wrong window tried", 200*time.Millisecond)

	sendCmd(t, one, command{Cmd: "approveBaton", Text: a.Action.Send["text"]})
	select {
	case <-result:
	case <-time.After(30 * time.Second):
		t.Fatal("the first window's approval did not go through")
	}
	var told approvalNotice
	for !strings.Contains(told.Text, "Another window allowed") {
		readUntil(t, two, "notice", &told)
	}
	// Its own token is dead now.
	sendCmd(t, two, command{Cmd: "approveBaton", Text: b.Action.Send["text"]})
	var dead approvalNotice
	for !(dead.Error && strings.Contains(dead.Text, "run out")) {
		readUntil(t, two, "notice", &dead)
	}
}

func TestABatonNotAllowedInTimeIsNotSent(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialWindow(t, srv)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 300*time.Millisecond)
	err := <-spawnElsewhere(hook, pane)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("err = %v, want a refusal for want of approval", err)
	}
	// The window is told to take the notice away.
	var gone struct {
		ID string `json:"id"`
	}
	readUntil(t, conn, "noticeWithdraw", &gone)
}

// When the agent's own request ends, the notice is withdrawn and the token stops
// working.
func TestACancelledRequestWithdrawsTheNoticeAndTheToken(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialWindow(t, srv)
	shortApprovalWait(t, 20*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- srv.approveElsewhere(ctx, approvalInfo{Asker: "A pane", Dest: "x", Source: "y"}) }()
	n := nextApproval(t, conn)
	cancel()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("err = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a cancelled request kept waiting")
	}
	var gone struct {
		ID string `json:"id"`
	}
	readUntil(t, conn, "noticeWithdraw", &gone)
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	var dead approvalNotice
	for !dead.Error {
		readUntil(t, conn, "notice", &dead)
	}
}

// An API agent's endpoint can carry a credential: it is never said.
func TestAnEndpointIsNotShownInTheNoticeOrTheRefusal(t *testing.T) {
	if got := shownProvider("openai via https:" + "//user:sekret@gw.example/v1"); strings.Contains(got, "sekret") || strings.Contains(got, "gw.example") {
		t.Errorf("shownProvider = %q", got)
	}
	got := providerChange("a", "openai via https:"+"//user:sekret@gw.example/v1", "b", "anthropic")
	if strings.Contains(got, "sekret") || strings.Contains(got, "gw.example") {
		t.Errorf("providerChange = %q", got)
	}
}

// An agent whose company is not known is described by why, never by empty
// brackets, and an endpoint is only ever its host.
func TestAnUnknownCompanyIsNeverShownAsEmptyBrackets(t *testing.T) {
	for _, tc := range []struct{ provider, why, want string }{
		{"", "", "company not known"},
		{"", "through a gateway or proxy (gw.example)", "through a gateway or proxy (gw.example)"},
		{"anthropic", "", "anthropic"},
		{"openai via https:" + "//user:sekret@gw.example/v1", "", "openai via gw.example"},
	} {
		got := providerText(tc.provider, tc.why)
		if got != tc.want || strings.Contains(got, "sekret") || strings.Contains(got, "()") {
			t.Errorf("providerText(%q, %q) = %q, want %q", tc.provider, tc.why, got, tc.want)
		}
	}
	if got := shownProvider(""); !strings.Contains(got, "not known") {
		t.Errorf("shownProvider(\"\") = %q", got)
	}
}

// The catalog can change while the user is being asked. What was approved is what
// runs: if the agent would now be another company's, nothing starts.
func TestAnAgentChangedWhileTheUserDecidesIsNotStarted(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	conn := dialWindow(t, srv)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 20*time.Second)
	result := spawnElsewhere(hook, pane)
	n := nextApproval(t, conn)

	// Another company's CLI takes the place of the approved one.
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version": 1, "agents": [{"id": "gocli", "name": "Go", "exe": "go", "args": [{"value": "{{session}}"}], "caps": {"resume": true}},
		{"id": "gitcli", "name": "Git as an agent", "exe": "go", "args": [{"value": "{{session}}"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ask(srv, func() bool { ws.ReloadAgents(); return true }); !ok {
		t.Fatal("the workspace did not answer")
	}
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "settings changed") {
			t.Errorf("err = %v, want the spawn refused for the change", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the spawn never answered")
	}
}

// A request that ends between a window claiming it and the spawn being told is not
// reported as sent: the window is told it ran out and the spawn is refused.
func TestARequestThatEndsAsItIsApprovedIsNotReportedAsSent(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialWindow(t, srv)
	shortApprovalWait(t, 20*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- srv.approveElsewhere(ctx, approvalInfo{Asker: "A pane", Dest: "x", Source: "y"}) }()
	n := nextApproval(t, conn)

	old := approveClaimed
	approveClaimed = func() {
		cancel()
		time.Sleep(150 * time.Millisecond) // the request sees it and ends
	}
	t.Cleanup(func() { approveClaimed = old })
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})

	var said approvalNotice
	for !strings.Contains(said.Text, "Approved") && !strings.Contains(said.Text, "run out") {
		readUntil(t, conn, "notice", &said)
	}
	if strings.Contains(said.Text, "Approved") || strings.Contains(said.Text, "Sending") || !said.Error {
		t.Errorf("a request that ended was reported as sent: %+v", said)
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("err = %v, want the cancelled request refused", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the request never ended")
	}
}
