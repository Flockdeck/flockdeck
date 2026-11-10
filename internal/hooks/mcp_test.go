package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/team"
)

// rawMCP posts body to path with the given bearer token and returns the status
// and the answer.
func rawMCP(t *testing.T, srv *Server, path, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

type msgSpy struct {
	mu   sync.Mutex
	reqs []MsgRequest
}

func (m *msgSpy) handler(r MsgRequest) (MsgResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs = append(m.reqs, r)
	return MsgResult{Sent: []Sent{{ID: len(m.reqs), Pane: "p9", To: "rev"}}}, nil
}

func (m *msgSpy) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reqs)
}

func TestMCPSendActsAsThePaneOfTheToken(t *testing.T) {
	srv, _ := newServer(t)
	spy := &msgSpy{}
	srv.SetMsgHandler(spy.handler)
	a, b := srv.IssuePaneToken("pA"), srv.IssuePaneToken("pB")
	if a == "" || a == b {
		t.Fatalf("tokens = %q, %q", a, b)
	}
	if _, err := MCPSend(context.Background(), srv.BaseURL(), a, "rev", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := MCPSend(context.Background(), srv.BaseURL(), b, "rev", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(spy.reqs) != 2 || spy.reqs[0].Pane != "pA" || spy.reqs[1].Pane != "pB" || spy.reqs[0].Target != "rev" || spy.reqs[0].Text != "hello" {
		t.Errorf("the handler saw %+v, want one send as pA and one as pB", spy.reqs)
	}
}

func TestMCPSendRefusesABodyThatNamesASender(t *testing.T) {
	srv, _ := newServer(t)
	spy := &msgSpy{}
	srv.SetMsgHandler(spy.handler)
	a := srv.IssuePaneToken("pA")
	srv.IssuePaneToken("pB")
	for _, body := range []string{
		`{"pane":"pB","to":"rev","text":"hi"}`,
		`{"from":"pB","to":"rev","text":"hi"}`,
		`{"to":"rev","text":"hi","Pane":"pB"}`,
		`{"to":"rev","text":"hi","token":"x"}`,
	} {
		code, out := rawMCP(t, srv, PathMCPSend, a, body)
		if code != http.StatusBadRequest || !strings.Contains(out, "cannot be set") {
			t.Errorf("%s = %d %q, want a 400 that says the sender cannot be set", body, code, out)
		}
	}
	if spy.count() != 0 {
		t.Errorf("a spoofed send reached the handler: %+v", spy.reqs)
	}
}

func TestMCPEndpointsRefuseEveryTokenButAPaneToken(t *testing.T) {
	srv, _ := newServer(t)
	spy := &msgSpy{}
	srv.SetMsgHandler(spy.handler)
	srv.SetTeamHandler(func(TeamRequest) (TeamResult, error) { spy.handler(MsgRequest{}); return TeamResult{}, nil })
	a := srv.IssuePaneToken("pA")
	body := `{"to":"rev","text":"hi"}`
	for name, tok := range map[string]string{"none": "", "the run's shared token": srv.Token(), "a guess": "00" + a[2:], "blank bearer": " "} {
		for _, path := range []string{PathMCPSend, PathMCPPeers} {
			if code, _ := rawMCP(t, srv, path, tok, body); code != http.StatusForbidden {
				t.Errorf("%s on %s = %d, want 403", name, path, code)
			}
		}
	}
	// A pane's token is good for these two endpoints and no other.
	for _, path := range []string{"/msg", "/team", "/inbox", "/spawn", "/close", "/task", "/pane-name"} {
		code, _ := rawMCP(t, srv, path, a, `{"pane":"pA","token":"`+a+`"}`)
		if code == http.StatusOK {
			t.Errorf("a pane token worked on %s", path)
		}
	}
	if spy.count() != 0 {
		t.Errorf("a refused call reached a handler: %+v", spy.reqs)
	}
	if code, _ := rawMCP(t, srv, PathMCPSend, a, body); code != http.StatusOK {
		t.Errorf("the pane's own token = %d", code)
	}
}

func TestMCPTokenEndsWithTheSessionAndThePane(t *testing.T) {
	srv, _ := newServer(t)
	srv.SetMsgHandler((&msgSpy{}).handler)
	old := srv.IssuePaneToken("pA")
	fresh := srv.IssuePaneToken("pA")
	if code, _ := rawMCP(t, srv, PathMCPSend, old, `{"to":"x","text":"y"}`); code != http.StatusForbidden {
		t.Errorf("the token of an earlier session = %d, want 403", code)
	}
	if code, _ := rawMCP(t, srv, PathMCPSend, fresh, `{"to":"x","text":"y"}`); code != http.StatusOK {
		t.Errorf("the new token = %d", code)
	}
	srv.RevokePaneToken("pA")
	if code, _ := rawMCP(t, srv, PathMCPSend, fresh, `{"to":"x","text":"y"}`); code != http.StatusForbidden {
		t.Errorf("the token of a closed pane = %d, want 403", code)
	}
	if srv.IssuePaneToken("") != "" {
		t.Error("a token was issued for no pane")
	}
}

func TestMCPSendRefusesAnOversizeMessageBeforeTheHandler(t *testing.T) {
	srv, _ := newServer(t)
	spy := &msgSpy{}
	srv.SetMsgHandler(spy.handler)
	a := srv.IssuePaneToken("pA")
	if _, err := MCPSend(context.Background(), srv.BaseURL(), a, "rev", strings.Repeat("x", team.MaxText)); err != nil {
		t.Errorf("a message of exactly the limit: %v", err)
	}
	_, err := MCPSend(context.Background(), srv.BaseURL(), a, "rev", strings.Repeat("x", team.MaxText+1))
	if err == nil || !strings.Contains(err.Error(), "limit is 8192") {
		t.Errorf("an oversize message = %v", err)
	}
	// A body far past any message is cut off before it is parsed.
	huge := `{"to":"rev","text":"` + strings.Repeat("y", maxMCPBody) + `"}`
	if code, _ := rawMCP(t, srv, PathMCPSend, a, huge); code != http.StatusRequestEntityTooLarge {
		t.Errorf("a huge body = %d, want 413", code)
	}
	if spy.count() != 1 {
		t.Errorf("handler ran %d times, want only for the message that fit", spy.count())
	}
}

func TestMCPSendNeedsBothFields(t *testing.T) {
	srv, _ := newServer(t)
	spy := &msgSpy{}
	srv.SetMsgHandler(spy.handler)
	a := srv.IssuePaneToken("pA")
	for _, body := range []string{`{}`, `{"to":"rev"}`, `{"text":"hi"}`, `{"to":"  ","text":"hi"}`, `{"to":1,"text":"hi"}`, `{"to":"a","text":["b"]}`, `[]`, `null`, `{"to":"a","text":"b"}{"x":1}`, `nonsense`} {
		if code, _ := rawMCP(t, srv, PathMCPSend, a, body); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, code)
		}
	}
	if spy.count() != 0 {
		t.Errorf("a bad body reached the handler: %+v", spy.reqs)
	}
}

func TestMCPSendIsRateLimitedPerPane(t *testing.T) {
	srv, _ := newServer(t)
	spy := &msgSpy{}
	srv.SetMsgHandler(spy.handler)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	srv.mcpNow = func() time.Time { return now }
	a, b := srv.IssuePaneToken("pA"), srv.IssuePaneToken("pB")
	send := func(tok string) int {
		code, _ := rawMCP(t, srv, PathMCPSend, tok, `{"to":"rev","text":"hi"}`)
		return code
	}
	for i := 0; i < sendBurst; i++ {
		if code := send(a); code != http.StatusOK {
			t.Fatalf("send %d = %d", i, code)
		}
	}
	if code := send(a); code != http.StatusTooManyRequests {
		t.Fatalf("send past the burst = %d, want 429", code)
	}
	// Another pane has its own allowance.
	if code := send(b); code != http.StatusOK {
		t.Errorf("pane B was limited by pane A: %d", code)
	}
	// A refused call counts: a bad body does not earn the pane a free retry.
	if code, _ := rawMCP(t, srv, PathMCPSend, a, `{"pane":"pB"}`); code != http.StatusTooManyRequests {
		t.Errorf("a spoofed send while limited = %d, want 429", code)
	}
	now = now.Add(sendEvery)
	if code := send(a); code != http.StatusOK {
		t.Errorf("send after one interval = %d, want 200", code)
	}
	if code := send(a); code != http.StatusTooManyRequests {
		t.Errorf("a second send in the same interval = %d, want 429", code)
	}
	// A long wait refills the bucket to the burst and no further.
	now = now.Add(time.Hour)
	ok := 0
	for send(a) == http.StatusOK {
		ok++
	}
	if ok != sendBurst {
		t.Errorf("after an hour %d sends passed, want %d", ok, sendBurst)
	}
	if spy.count() != sendBurst+1+1+sendBurst {
		t.Errorf("handler ran %d times", spy.count())
	}
}

func TestMCPPeersShowsOnlyWhatAMessageNeeds(t *testing.T) {
	srv, _ := newServer(t)
	var asked []string
	srv.SetTeamHandler(func(r TeamRequest) (TeamResult, error) {
		asked = append(asked, r.Pane)
		return TeamResult{Members: []team.Member{
			{Pane: "pA", Name: "repo", Peer: "ada", Role: "worker", Agent: "claude", Model: "secret-model", Status: "idle", Self: true},
			{Pane: "pB", Name: "repo", Called: "reviewer of x", Role: "reviewer", Status: "working"},
		}}, nil
	})
	a := srv.IssuePaneToken("pA")
	res, err := MCPListPeers(context.Background(), srv.BaseURL(), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "pA" {
		t.Errorf("the roster was asked for %v, want pA", asked)
	}
	if len(res.Peers) != 2 || !res.Peers[0].Self || res.Peers[0].Label != "ada" || res.Peers[1].Label != "reviewer of x" || res.Peers[1].Role != "reviewer" {
		t.Errorf("peers = %+v", res.Peers)
	}
	_, raw := rawMCP(t, srv, PathMCPPeers, a, `{}`)
	var keys map[string][]map[string]any
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"pane": true, "label": true, "role": true, "agent": true, "status": true, "self": true}
	for _, p := range keys["peers"] {
		for k := range p {
			if !allowed[k] {
				t.Errorf("list_peers shows %q", k)
			}
		}
	}
	if strings.Contains(raw, a) || strings.Contains(raw, srv.Token()) || strings.Contains(raw, "secret-model") {
		t.Errorf("list_peers leaked: %s", raw)
	}
	if code, _ := rawMCP(t, srv, PathMCPPeers, a, `{"pane":"pB"}`); code != http.StatusBadRequest {
		t.Errorf("list_peers with a pane argument = %d, want 400", code)
	}
}

func TestMCPPeersIsRateLimited(t *testing.T) {
	srv, _ := newServer(t)
	srv.SetTeamHandler(func(TeamRequest) (TeamResult, error) { return TeamResult{}, nil })
	now := time.Now()
	srv.mcpNow = func() time.Time { return now }
	a := srv.IssuePaneToken("pA")
	for i := 0; i < peersBurst; i++ {
		if code, _ := rawMCP(t, srv, PathMCPPeers, a, `{}`); code != http.StatusOK {
			t.Fatalf("call %d = %d", i, code)
		}
	}
	if code, _ := rawMCP(t, srv, PathMCPPeers, a, `{}`); code != http.StatusTooManyRequests {
		t.Errorf("past the burst = %d, want 429", code)
	}
}

func TestMCPEndpointsSayWhenNothingIsInstalled(t *testing.T) {
	srv, _ := newServer(t)
	a := srv.IssuePaneToken("pA")
	if code, _ := rawMCP(t, srv, PathMCPSend, a, `{"to":"a","text":"b"}`); code != http.StatusServiceUnavailable {
		t.Errorf("send = %d", code)
	}
	if code, _ := rawMCP(t, srv, PathMCPPeers, a, `{}`); code != http.StatusServiceUnavailable {
		t.Errorf("peers = %d", code)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.BaseURL()+PathMCPSend, nil)
	req.Header.Set("Authorization", "Bearer "+a)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d", resp.StatusCode)
	}
}

func TestMCPSpawnActsAsThePaneOfTheTokenAndMirrorsTheCLI(t *testing.T) {
	srv, _ := newServer(t)
	var seen []SpawnRequest
	srv.SetSpawnHandler(func(r SpawnRequest) (SpawnResult, error) {
		seen = append(seen, r)
		return SpawnResult{PaneID: "p9", Cwd: "/wt", Branch: r.Branch, Role: r.Role}, nil
	})
	a, b := srv.IssuePaneToken("pA"), srv.IssuePaneToken("pB")
	res, err := MCPSpawn(context.Background(), srv.BaseURL(), a, MCPSpawnBody{
		Task: " build it ", Role: "worker", Name: "the build", Tab: "work", Worktree: "feat/x", Split: true, Agent: "claude", Model: "m", Terminal: true,
	})
	if err != nil || res.PaneID != "p9" || res.Branch != "feat/x" {
		t.Fatalf("spawn = %+v, %v", res, err)
	}
	if _, err := MCPSpawn(context.Background(), srv.BaseURL(), b, MCPSpawnBody{Task: "t", NoWorktree: true}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("the handler saw %d requests", len(seen))
	}
	g := seen[0]
	if g.Parent != "pA" || g.Task != "build it" || g.Role != "worker" || g.Label != team.PaneLabelText("the build") || g.Tab != "work" ||
		g.Branch != "feat/x" || !g.Split || g.Agent != "claude" || g.Model != "m" || !g.Terminal || g.Ctx == nil {
		t.Errorf("first request = %+v", g)
	}
	if g.Shell || g.Baton != "" || g.TaskID != "" || g.Record || g.BatonElsewhere || g.Token != "" {
		t.Errorf("a field the tool does not offer was set: %+v", g)
	}
	if seen[1].Parent != "pB" || !seen[1].NoWorktree {
		t.Errorf("second request = %+v", seen[1])
	}
}

func TestMCPSpawnRefusesWhatTheCLIRefuses(t *testing.T) {
	srv, _ := newServer(t)
	calls := 0
	srv.SetSpawnHandler(func(SpawnRequest) (SpawnResult, error) { calls++; return SpawnResult{PaneID: "p"}, nil })
	tok := srv.IssuePaneToken("pA")
	for name, body := range map[string]MCPSpawnBody{
		"no task":              {},
		"blank task":           {Task: "  "},
		"worktree and none":    {Task: "x", Worktree: "b", NoWorktree: true},
		"tab with a line feed": {Task: "x", Tab: "a\nb"},
		"tab too long":         {Task: "x", Tab: strings.Repeat("t", MaxTabName+1)},
	} {
		if _, err := MCPSpawn(context.Background(), srv.BaseURL(), tok, body); err == nil {
			t.Errorf("%s: spawned", name)
		}
	}
	if calls != 0 {
		t.Errorf("the handler ran %d times for refused requests", calls)
	}
}

func TestMCPSpawnRefusesFieldsAndTokensItDoesNotKnow(t *testing.T) {
	srv, _ := newServer(t)
	calls := 0
	srv.SetSpawnHandler(func(SpawnRequest) (SpawnResult, error) { calls++; return SpawnResult{PaneID: "p"}, nil })
	tok := srv.IssuePaneToken("pA")
	// A refused call counts against the limit too, so the clock moves on.
	now := time.Unix(1000, 0)
	srv.mcpNow = func() time.Time { return now }
	for _, body := range []string{
		`{"task":"x","parent":"pB"}`, `{"task":"x","token":"t"}`, `{"task":"x","shell":true}`,
		`{"task":"x","baton":"self"}`, `{"task":"x","taskId":"1"}`, `{"task":"x","record":true}`,
	} {
		now = now.Add(spawnEvery)
		if code, _ := rawMCP(t, srv, PathMCPSpawn, tok, body); code != 400 {
			t.Errorf("%s = %d, want 400", body, code)
		}
	}
	if code, _ := rawMCP(t, srv, PathMCPSpawn, "", `{"task":"x"}`); code != 403 {
		t.Errorf("no token = %d, want 403", code)
	}
	if code, _ := rawMCP(t, srv, PathMCPSpawn, "nope", `{"task":"x"}`); code != 403 {
		t.Errorf("wrong token = %d, want 403", code)
	}
	if calls != 0 {
		t.Errorf("the handler ran %d times", calls)
	}
}

func TestMCPSpawnWithoutAHandlerIsUnavailable(t *testing.T) {
	srv, _ := newServer(t)
	tok := srv.IssuePaneToken("pA")
	if code, _ := rawMCP(t, srv, PathMCPSpawn, tok, `{"task":"x"}`); code != 503 {
		t.Errorf("= %d, want 503", code)
	}
}

func TestMCPSpawnPassesTheRefusalOfTheHandlerOn(t *testing.T) {
	srv, _ := newServer(t)
	srv.SetSpawnHandler(func(SpawnRequest) (SpawnResult, error) {
		return SpawnResult{}, errors.New("a worker may not start a manager")
	})
	tok := srv.IssuePaneToken("pA")
	_, err := MCPSpawn(context.Background(), srv.BaseURL(), tok, MCPSpawnBody{Task: "x", Role: "manager"})
	if err == nil || !strings.Contains(err.Error(), "may not start a manager") {
		t.Errorf("err = %v", err)
	}
}

func TestMCPSpawnIsRateLimitedPerPane(t *testing.T) {
	srv, _ := newServer(t)
	now := time.Unix(1000, 0)
	srv.mcpNow = func() time.Time { return now }
	srv.SetSpawnHandler(func(SpawnRequest) (SpawnResult, error) { return SpawnResult{PaneID: "p"}, nil })
	a, b := srv.IssuePaneToken("pA"), srv.IssuePaneToken("pB")
	for i := 0; i < spawnBurst; i++ {
		if code, _ := rawMCP(t, srv, PathMCPSpawn, a, `{"task":"x"}`); code != 200 {
			t.Fatalf("spawn %d = %d", i, code)
		}
	}
	if code, _ := rawMCP(t, srv, PathMCPSpawn, a, `{"task":"x"}`); code != 429 {
		t.Errorf("over the burst = %d, want 429", code)
	}
	if code, _ := rawMCP(t, srv, PathMCPSpawn, b, `{"task":"x"}`); code != 200 {
		t.Errorf("another pane = %d, want its own bucket", code)
	}
	// The send bucket is separate.
	if code, _ := rawMCP(t, srv, PathMCPPeers, a, `{}`); code == 429 {
		t.Error("list_peers shares the spawn bucket")
	}
	now = now.Add(spawnEvery)
	if code, _ := rawMCP(t, srv, PathMCPSpawn, a, `{"task":"x"}`); code != 200 {
		t.Errorf("after the refill = %d, want 200", code)
	}
}
