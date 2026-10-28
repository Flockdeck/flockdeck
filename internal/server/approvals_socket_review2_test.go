package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

func shownLines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range approvalLog(t) {
		if l["event"] == "shown" {
			out = append(out, l)
		}
	}
	return out
}

// An agent that replaces its card every tick must not fill the log, and the lines
// about cards are not in the file that holds the decisions.
func TestAnAgentReplacingItsCardEveryTickCannotFloodTheLogs(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	p.list()
	p.decide(p.list()[0], "deny")
	for i := 0; i < 150; i++ {
		e.pane.set(goodCard(uint64(100+i), "echo "+strconv.Itoa(i)))
		time.Sleep(25 * time.Millisecond)
	}
	if n := len(shownLines(t)); n == 0 || n > approvalShownPerMinute+1 {
		t.Errorf("%d shown lines, want between 1 and %d", n, approvalShownPerMinute)
	}
	data, err := os.ReadFile(filepath.Join(approvalDir(t), approvalAuditFile))
	if err != nil || strings.Contains(string(data), `"event":"shown"`) {
		t.Errorf("shown lines are in the decisions file (%v)", err)
	}
	if !strings.Contains(string(data), `"event":"decision"`) {
		t.Error("the decision is not in the decisions file")
	}
}

func TestACardTheDeviceCannotAnswerIsNotLoggedAsShown(t *testing.T) {
	e := newApprovalsEnv(t)
	c := goodCard(7, "plan")
	c.Kind = "switch_mode"
	e.pane.set(c)
	p := e.open(t)
	if it := p.list(); len(it) != 1 || it[0]["answerable"] == true {
		t.Fatalf("list %v", it)
	}
	if n := len(shownLines(t)); n != 0 {
		t.Errorf("%d shown lines for a card the phone cannot answer", n)
	}
}

// A refused tap has the same card offered again; the card is logged once.
func TestACardOfferedAgainAfterARefusedTapIsLoggedOnce(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	for i := 0; i < approvalAbuseMax-1; i++ {
		d := decideFor(p.list()[0], "allow")
		d["token"] = "8.0"
		p.send(d)
		p.readOp("approvalResult")
	}
	if got := p.list(); len(got) != 1 {
		t.Fatalf("list %v", got)
	}
	if n := len(shownLines(t)); n != 1 {
		t.Errorf("%d shown lines for one card", n)
	}
}

// Cutting a socket is the desk's doing: it ends with no refusal line and no
// notice of a refusal.
func TestADeskCutClosesTheSocketQuietly(t *testing.T) {
	e := newApprovalsEnv(t)
	desk := dialControlPage(t, e.srv)
	p := e.open(t)
	p.list()
	e.srv.sockets.cut("d1")
	if code, reason := p.closed(); code != artifactCloseRefused || reason != approvalReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		_, data, err := desk.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		if strings.Contains(string(data), "refused phone approval") {
			t.Fatalf("the desk was told of a refusal: %.120s", data)
		}
	}
	if approvalLogHas(t, "denied", "") {
		t.Errorf("a refusal was logged: %v", approvalLog(t))
	}
}

// A device that is refused with no cut (its record changed, nothing cut it) is
// still a refusal.
func TestARefusalWithNoCutIsStillRecorded(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	p.list()
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull})
	p.sendQuiet(map[string]any{"op": "ping"})
	if code, reason := p.closed(); code != artifactCloseRefused || reason != approvalReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
	if !approvalLogHas(t, "denied", approvalReasonDisabled) {
		t.Error("no denied line")
	}
}

// The line that says an answer was not given after the decision line was written
// is not held back by the limiter, even when the same reason was just logged.
func TestTheRefusalAfterADecisionLineIsAlwaysWritten(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.setTimings(func(tm *approvalTimings) {
		tm.hook = func(stage string) {
			if stage == "after-audit" {
				e.srv.approvalRefused(auditEvent{Event: "decision-refused", Device: "d1", Reason: approvalReasonDisabled})
				e.srv.sockets.cut("d1")
			}
		}
	})
	p := e.open(t)
	p.send(decideFor(p.list()[0], "allow"))
	p.closed()
	if len(e.pane.given()) != 0 {
		t.Fatalf("an answer was given: %v", e.pane.given())
	}
	// The cut is the desk's doing, so the socket's closing is not logged as a refusal.
	if approvalLogHas(t, "denied", "") {
		t.Errorf("a refusal was logged: %v", approvalLog(t))
	}
	waitUntil(t, "the refusal after the decision line", func() bool {
		for _, l := range approvalLog(t) {
			if l["event"] == "decision-refused" && l["reason"] == approvalReasonDisabled && l["pane"] == e.paneID {
				return true
			}
		}
		return false
	})
}

// The same, through the desk's own kill switch.
func TestTheKillSwitchDuringADecisionStopsTheAnswer(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.setTimings(func(tm *approvalTimings) {
		tm.hook = func(stage string) {
			if stage == "after-audit" {
				done := make(chan struct{})
				e.srv.stopApprovals(func(int, error) { close(done) })
				<-done
			}
		}
	})
	p := e.open(t)
	p.send(decideFor(p.list()[0], "allow"))
	if code, reason := p.closed(); code != artifactCloseRefused || reason != approvalReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
	if len(e.pane.given()) != 0 {
		t.Fatalf("an answer was given: %v", e.pane.given())
	}
}

// Opening is recorded when the first request opens, not at the handshake.
func TestTheConnectLineWaitsForTheFirstRequest(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	time.Sleep(300 * time.Millisecond)
	if approvalLogHas(t, "connect", "") {
		t.Fatal("connect was written before any request")
	}
	p.send(map[string]any{"op": "ping"})
	p.readOp("pong")
	if !approvalLogHas(t, "connect", "") {
		t.Error("no connect line after the first request")
	}
}

// An offer of the right shape that this socket never made is what a phone holds
// after a reconnect: stale, not abuse. A malformed one is abuse.
func TestAnOfferFromAnEarlierSocketIsStaleNotAbuse(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	d := decideFor(p.list()[0], "allow")
	d["offer"] = strings.Repeat("a", 32)
	for i := 0; i < approvalAbuseMax+2; i++ {
		p.send(d)
		if res := p.readOp("approvalResult"); res["ok"] == true || res["error"] != permissionStale {
			t.Fatalf("got %v", res)
		}
	}
	if got := p.list(); len(got) != 1 {
		t.Errorf("the socket was ended: %v", got)
	}
}

func TestAMalformedOfferIsAbuse(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	d := decideFor(p.list()[0], "allow")
	d["offer"] = "not-an-offer"
	for i := 0; i < approvalAbuseMax; i++ {
		p.send(d)
		p.readOp("approvalResult")
	}
	if code, reason := p.closed(); code != artifactCloseRefused || reason != "refused" {
		t.Errorf("closed with %d %q", code, reason)
	}
}

// Only a successful answer starts the count again; other valid requests do not.
func TestOtherValidRequestsDoNotStartTheCountOfRefusalsAgain(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	d := decideFor(p.list()[0], "allow")
	d["offer"] = "not-an-offer"
	for i := 0; i < approvalAbuseMax-1; i++ {
		p.send(d)
		p.readOp("approvalResult")
		p.send(map[string]any{"op": "ping"})
		p.readOp("pong")
	}
	p.send(d)
	if code, reason := p.closed(); code != artifactCloseRefused || reason != "refused" {
		t.Errorf("closed with %d %q", code, reason)
	}
}
