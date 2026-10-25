package server

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/changes"
)

func TestSameShaTakesAnAbbreviationOfAtLeastSevenCharacters(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{full, full, true}, {full[:7], full, true}, {full, full[:12], true},
		{full[:6], full, false}, {"", full, false}, {"fedcba9876", full, false},
	} {
		if sameSha(c.a, c.b) != c.want {
			t.Errorf("sameSha(%q, %q) = %v", c.a, c.b, !c.want)
		}
	}
}

// A result is shown as it was only for the head it ran on; for any other it is
// queued, never a pass.
func TestACheckResultIsNeverShownAsAPassForAnotherHead(t *testing.T) {
	var r checkRuns
	r.put("p1", changes.Check{Name: "unit", State: "pass", Source: "local", AtSha: "aaaaaaa", Summary: "ok", Worktree: true})
	got := r.of("p1", "aaaaaaa")
	if len(got) != 1 || got[0].State != "pass" || !got[0].Worktree {
		t.Errorf("same head: %+v", got)
	}
	got = r.of("p1", "bbbbbbb")
	if len(got) != 1 || got[0].State != "queued" || got[0].AtSha != "bbbbbbb" || got[0].Summary != "" || got[0].Worktree {
		t.Errorf("another head: %+v, want queued with nothing carried over", got)
	}
	if got := r.of("p2", "aaaaaaa"); got == nil || len(got) != 0 {
		t.Errorf("a pane with no result: %v, want an empty list", got)
	}
	if !r.begin("p1") || r.begin("p1") {
		t.Error("a second run for a pane was allowed")
	}
	r.end("p1")
	if !r.begin("p1") {
		t.Error("a run was refused after the first ended")
	}
	r.end("p1")
	r.forget(map[string]bool{})
	if got := r.of("p1", "aaaaaaa"); len(got) != 0 {
		t.Errorf("results of a pane that is gone: %+v", got)
	}
}

func TestChecksAndReviewsRefuseWhatTheyCannotAnswer(t *testing.T) {
	srv, _ := newTestServer(t)
	for name, c := range map[string]struct {
		cmd  command
		code string
	}{
		"runChecks no id":           {command{Cmd: "runChecks", Req: "r"}, "badRequest"},
		"runChecks no pane":         {command{Cmd: "runChecks", Req: "r", ID: "nope"}, "noBranch"},
		"requestReview no reviewer": {command{Cmd: "requestReview", Req: "r", ID: "p1"}, "badRequest"},
		"requestReview own branch":  {command{Cmd: "requestReview", Req: "r", ID: "p1", Reviewer: "p1"}, "badRequest"},
		"requestReview no pane":     {command{Cmd: "requestReview", Req: "r", ID: "nope", Reviewer: "p2"}, "noBranch"},
		"submitReview bad state":    {command{Cmd: "submitReview", Req: "r", ID: "p1", State: "merged", ExpectSha: "abcdef0"}, "badRequest"},
		"submitReview no expectSha": {command{Cmd: "submitReview", Req: "r", ID: "p1", State: "approved"}, "badRequest"},
		"submitReview too long":     {command{Cmd: "submitReview", Req: "r", ID: "p1", State: "approved", ExpectSha: "abcdef0", Note: strings.Repeat("n", changes.MaxNote+1)}, "badRequest"},
		"submitReview no pane":      {command{Cmd: "submitReview", Req: "r", ID: "nope", State: "approved", ExpectSha: "abcdef0"}, "noBranch"},
	} {
		t.Run(name, func(t *testing.T) {
			cl := &controlClient{out: make(chan []byte, 8), page: true}
			srv.handleCommand(cl, c.cmd)
			m := answerOf(t, cl)
			if m.Type != "changesResult" || m.Op != c.cmd.Cmd || m.Req != "r" || m.OK || m.Code != c.code || m.Error == "" {
				t.Errorf("answer = %+v, want a %s refusal carrying op and req", m, c.code)
			}
		})
	}
}

// None of the three is reachable from a window through the relay, and a shared
// fixture has the caps in the hello of a window on this machine only.
func TestChecksAndReviewsAreRefusedToARelayWindow(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, name := range []string{"runChecks", "requestReview", "submitReview"} {
		cl := &controlClient{out: make(chan []byte, 8), remote: true, device: "d"}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "p1", Reviewer: "p2", State: "approved", ExpectSha: "abcdef0"})
		if m := answerOf(t, cl); m.Code != "deskOnly" || m.Op != name {
			t.Errorf("%s: answer = %+v, want deskOnly", name, m)
		}
	}
	if !slices.Contains(apiFor(false).Caps, "changesChecks") || slices.Contains(apiFor(true).Caps, "changesChecks") {
		t.Error("changesChecks is not listed for this machine only")
	}
}

// The request is worded as Flockdeck's, names the branch and head, approves
// nothing, and quotes a note after cleaning it.
func TestAReviewRequestSaysWhatWasAskedAndApprovesNothing(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	got := reviewRequestText("feat/x", sha, "look at the\u202e parser\x07\nand the tests")
	for _, want := range []string{"the person asked you to review feat/x at 0123456789ab.", "> look at the parser", "> and the tests", "It does not approve or merge anything."} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"from the person", "\u202e", "\x07", "please merge", "approved"} {
		if strings.Contains(got, bad) {
			t.Errorf("text holds %q:\n%s", bad, got)
		}
	}
	if plain := reviewRequestText("feat/x", sha, ""); strings.Contains(plain, "quoted") {
		t.Errorf("a request with no note quotes one: %s", plain)
	}
	rec := reviewRecordedText("changes", "feat/x", sha, "no")
	if !strings.Contains(rec, "asked for changes on feat/x at 0123456789ab") || !strings.Contains(rec, "not a merge approval") || !strings.Contains(rec, "> no") {
		t.Errorf("recorded text = %s", rec)
	}
}

// The three commands act for the person, so a connection that did not open as a
// page of the window is refused them (an agent's token is accepted by the socket).
func TestChecksAndReviewsAreRefusedToAConnectionThatIsNotAPage(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, name := range []string{"runChecks", "requestReview", "submitReview"} {
		cl := &controlClient{out: make(chan []byte, 8)}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "p1", Reviewer: "p2", State: "approved", ExpectSha: "abcdef0"})
		if m := answerOf(t, cl); m.Code != "deskOnly" || m.Op != name || m.OK {
			t.Errorf("%s: answer = %+v, want deskOnly", name, m)
		}
	}
}

func TestAFailedListingOfBranchesMeansNoBranchIsGone(t *testing.T) {
	lists := map[string]map[string]bool{"a": {"main": true, "feat": true}, "b": {"main": true, "other": true}}
	list := func(d string) (map[string]bool, error) {
		if l, ok := lists[d]; ok {
			return l, nil
		}
		return nil, errors.New("not a repository")
	}
	got := unionBranches([]string{"a", "b"}, list)
	if !got["feat"] || !got["other"] || !got["main"] || got["gone"] {
		t.Errorf("a branch in only one checkout must count as live: %v", got)
	}
	if got := unionBranches([]string{"a", "broken"}, list); got != nil {
		t.Errorf("a failed listing gave %v, want nil", got)
	}
}

func TestADamagedStateFileIsReportedOnceUntilItLoads(t *testing.T) {
	reviewsMu.Lock()
	defer reviewsMu.Unlock()
	p := "damaged-test-state.json"
	defer delete(damagedReviews, p)
	bad := errors.New("damaged")
	if !firstDamage(p, bad) || firstDamage(p, bad) {
		t.Fatal("want one notice for the same damage")
	}
	if firstDamage(p, nil) {
		t.Fatal("a good load is not damage")
	}
	if !firstDamage(p, bad) {
		t.Error("damage after a good load is reported again")
	}
}

func TestAFailedLookAfterTheRunConfirmsNothing(t *testing.T) {
	failed := changes.Branch{Error: "git failed"}
	pass := changes.Check{Name: "unit", State: "pass", Source: "local", AtSha: "abc1234"}
	if got := confirmAfterRun(pass, "unit", "abc1234", failed, false); got.State != "pass" || !got.Worktree {
		t.Errorf("pass = %+v, want a pass marked worktree", got)
	}
	fail := changes.Check{Name: "unit", State: "fail", Source: "local", AtSha: "abc1234"}
	if got := confirmAfterRun(fail, "unit", "abc1234", failed, false); got.State != "queued" || got.AtSha != "abc1234" || got.Worktree {
		t.Errorf("fail = %+v, want queued", got)
	}
	moved := changes.Branch{HeadSha: "def5678"}
	if got := confirmAfterRun(pass, "unit", "abc1234", moved, false); got.State != "queued" || got.AtSha != "def5678" {
		t.Errorf("moved head = %+v", got)
	}
	same := changes.Branch{HeadSha: "abc1234"}
	if got := confirmAfterRun(pass, "unit", "abc1234", same, false); got.State != "pass" || got.Worktree {
		t.Errorf("confirmed pass = %+v", got)
	}
}
