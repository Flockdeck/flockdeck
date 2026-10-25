package hooks

import (
	"errors"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/team"
)

func TestTeamEndpointsCarryTheRequestAndTheAnswer(t *testing.T) {
	srv, _ := newServer(t)
	var gotTeam TeamRequest
	var gotMsg MsgRequest
	var gotInbox []InboxRequest
	srv.SetTeamHandler(func(r TeamRequest) (TeamResult, error) {
		gotTeam = r
		return TeamResult{Members: []team.Member{{Pane: "p1", Status: "idle", Self: true}}}, nil
	})
	srv.SetMsgHandler(func(r MsgRequest) (MsgResult, error) {
		gotMsg = r
		return MsgResult{Sent: []Sent{{ID: 7, Pane: "p2", To: "rev"}}}, nil
	})
	srv.SetInboxHandler(func(r InboxRequest) (InboxResult, error) {
		gotInbox = append(gotInbox, r)
		return InboxResult{Messages: []team.Message{{ID: 7, Text: "hi"}}}, nil
	})
	api, tok := srv.BaseURL(), srv.Token()

	res, err := Team(api, tok, "p1")
	if err != nil || len(res.Members) != 1 || !res.Members[0].Self || gotTeam.Pane != "p1" {
		t.Fatalf("team = %+v, %v, handler saw %+v", res, err, gotTeam)
	}
	sent, err := Msg(api, tok, "p1", "rev", "look at b")
	if err != nil || len(sent.Sent) != 1 || sent.Sent[0].ID != 7 {
		t.Fatalf("msg = %+v, %v", sent, err)
	}
	if gotMsg.Pane != "p1" || gotMsg.Target != "rev" || gotMsg.Text != "look at b" {
		t.Errorf("handler saw %+v", gotMsg)
	}
	if msgs, err := Inbox(api, tok, "p1", true); err != nil || len(msgs) != 1 {
		t.Fatalf("inbox = %+v, %v", msgs, err)
	}
	if m, err := ReadMessage(api, tok, "p1", 7); err != nil || m.ID != 7 {
		t.Fatalf("read = %+v, %v", m, err)
	}
	if len(gotInbox) != 2 || !gotInbox[0].All || gotInbox[0].Read != 0 || gotInbox[1].Read != 7 || gotInbox[1].Pane != "p1" {
		t.Errorf("inbox requests = %+v", gotInbox)
	}
}

func TestTeamEndpointsRefuseAWrongTokenWithoutRunningTheHandler(t *testing.T) {
	srv, _ := newServer(t)
	ran := false
	srv.SetTeamHandler(func(TeamRequest) (TeamResult, error) { ran = true; return TeamResult{}, nil })
	srv.SetMsgHandler(func(MsgRequest) (MsgResult, error) { ran = true; return MsgResult{}, nil })
	srv.SetInboxHandler(func(InboxRequest) (InboxResult, error) { ran = true; return InboxResult{}, nil })

	if _, err := Team(srv.BaseURL(), "wrong", "p1"); err == nil {
		t.Error("team accepted a wrong token")
	}
	if _, err := Msg(srv.BaseURL(), "wrong", "p1", "x", "y"); err == nil {
		t.Error("msg accepted a wrong token")
	}
	if _, err := Inbox(srv.BaseURL(), "wrong", "p1", false); err == nil {
		t.Error("inbox accepted a wrong token")
	}
	if _, err := ReadMessage(srv.BaseURL(), "", "p1", 1); err == nil {
		t.Error("read accepted an empty token")
	}
	if ran {
		t.Error("a handler ran for a request with a wrong token")
	}
}

func TestTeamEndpointsPassOnARefusalAndSayWhenNothingIsInstalled(t *testing.T) {
	srv, _ := newServer(t)
	if _, err := Team(srv.BaseURL(), srv.Token(), "p1"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("no handler: %v", err)
	}
	srv.SetMsgHandler(func(MsgRequest) (MsgResult, error) { return MsgResult{}, errors.New("no pane \"p9\" is open here") })
	if _, err := Msg(srv.BaseURL(), srv.Token(), "p9", "x", "y"); err == nil || !strings.Contains(err.Error(), `no pane "p9" is open here`) {
		t.Errorf("a refusal = %v, want the handler's words", err)
	}
}

func TestPaneNameCarriesTheTargetAndTheTextAndRefusesAWrongToken(t *testing.T) {
	srv, _ := newServer(t)
	var got PaneNameRequest
	srv.SetPaneNameHandler(func(r PaneNameRequest) (PaneNameResult, error) {
		got = r
		if r.Text == "refuse" {
			return PaneNameResult{}, errors.New("only a manager can name another pane")
		}
		return PaneNameResult{Pane: "p2", Label: "limiter PR"}, nil
	})
	api, tok := srv.BaseURL(), srv.Token()

	res, err := PaneName(api, tok, "p1", "p2", "limiter PR")
	if err != nil || res.Pane != "p2" || res.Label != "limiter PR" {
		t.Fatalf("PaneName = %+v, %v", res, err)
	}
	if got.Pane != "p1" || got.Target != "p2" || got.Text != "limiter PR" {
		t.Errorf("handler saw %+v", got)
	}
	if _, err := PaneName(api, tok, "p1", "", "refuse"); err == nil || !strings.Contains(err.Error(), "only a manager") {
		t.Errorf("a refusal reached the caller as %v", err)
	}
	if _, err := PaneName(api, "not-the-token", "p1", "", "x"); err == nil {
		t.Error("a wrong token was accepted")
	}
}
