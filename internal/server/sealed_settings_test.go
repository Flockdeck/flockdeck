package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/ghcli"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// Stage 8 of /ws/sealed (API 1.42, cap sealedSettings): push preferences (tier B,
// read at tier A), the cut-down device list and unpairing the device itself, and
// GitHub (reads at tier A for a full device, a comment at tier C). The rules held
// here: every change of who can reach or control this machine stays off the
// socket, a device can unpair only itself, no key or id of another device is
// sent, other people's text is cut and redacted, a comment is asked of the desk
// each time, and the log holds no text.

const settingsSecret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"

func TestSealedSettingsTables(t *testing.T) {
	for _, n := range []string{"pushPrefsGet", "remoteDevices", "remoteRevoke", "ghStatus", "ghPRs", "ghPR", "ghIssues", "ghIssue"} {
		if sealedCommands[n] != sealedTierA {
			t.Errorf("%s is tier %q, want A", n, sealedCommands[n])
		}
	}
	for _, n := range []string{"remoteDevices", "ghStatus", "ghPRs", "ghPR", "ghIssues", "ghIssue"} {
		if !sealedFullOnly[n] {
			t.Errorf("%s is not for full devices only", n)
		}
	}
	for _, n := range []string{"pushPrefsGet", "remoteRevoke"} {
		if sealedFullOnly[n] {
			t.Errorf("%s is for full devices only", n)
		}
	}
	if !sealedSelfOnly["remoteRevoke"] {
		t.Error("remoteRevoke is not held to the device itself")
	}
	for _, n := range []string{"pushNotify", "pushAnonymous", "pushDelay"} {
		if sealedCommands[n] != sealedTierB {
			t.Errorf("%s is tier %q, want B", n, sealedCommands[n])
		}
	}
	for _, n := range []string{"ghPRComment", "ghIssueComment"} {
		if sealedCommands[n] != sealedTierC || !isGitAction(n) {
			t.Errorf("%s is tier %q, want C and asked of the desk", n, sealedCommands[n])
		}
	}
	// What changes who can reach or control this machine, and the rest of GitHub.
	for _, n := range []string{"remotePair", "remoteRename", "remoteReconnect", "remoteVerify", "remoteUnverify", "remoteEnable",
		"remoteDisable", "remoteRemove", "remoteMove", "setDeviceRole", "setDeviceControl", "setDeviceApprove", "setApprovalsOff",
		"ghPRCreate", "ghIssueCreate", "ghLogin", "ghLoginCancel", "ghLogout", "ghInstall", "mergeBranch", "updates", "installVersion",
		"helperInstall", "keySet", "keyClear", "setAgentDefault", "theme", "setKeybinding", "recordPane"} {
		if sealedCommands[n] != "" {
			t.Errorf("%s is on the sealed socket", n)
		}
	}
	for _, typ := range []string{"pushPrefs", "deviceRoster", "deviceRevoked", "ghStatus", "ghPRs", "ghPR", "ghIssues", "ghIssue"} {
		if sealedMessages[typ].Tier != sealedTierA {
			t.Errorf("message %s is tier %q", typ, sealedMessages[typ].Tier)
		}
	}
	// The desk's own list, with keys, fingerprints and hosts, is never sent.
	for _, typ := range []string{"remoteDevices", "remotePair", "prefs"} {
		if sealedMessages[typ].Tier != "" {
			t.Errorf("message %s is sent to a sealed device", typ)
		}
	}
}

func pushPrefsNow(t *testing.T, p *sealedPhone, req string) map[string]any {
	t.Helper()
	p.send(command{Cmd: "pushPrefsGet", Req: req})
	return p.readType("pushPrefs")
}

// The preferences are a read for any device that is served; the three that
// change them need the desk's switch, and the answer says where they stand.
func TestSealedPushPrefs(t *testing.T) {
	e := newSealedEnv(t)
	p, hello := e.open(t, "full")
	if helloCommands(hello)["pushDelay"] {
		t.Error("a device without the switch is told it may change push preferences")
	}
	got := pushPrefsNow(t, p, "g")
	if got["req"] != "g" || got["off"] != false || got["anonymous"] != false || got["delaySeconds"] != float64(30) ||
		got["minSeconds"] != float64(5) || got["maxSeconds"] != float64(3600) || got["defaultSeconds"] != float64(30) {
		t.Errorf("preferences = %v", got)
	}
	for _, c := range []command{{Cmd: "pushNotify", Kind: "off"}, {Cmd: "pushAnonymous", Kind: "on"}, {Cmd: "pushDelay", Size: 120}} {
		c.Req = "r-" + c.Cmd
		p.send(c)
		if r := p.readType("sealedRefused"); r["code"] != sealedCodeControl || r["cmd"] != c.Cmd {
			t.Errorf("%s without the switch was answered %v", c.Cmd, r)
		}
	}
	if pp := e.srv.prefs.Push; pp.Off || pp.Anonymous || pp.DelaySeconds != 0 {
		t.Errorf("a refused device changed the preferences: %+v", pp)
	}

	e.grantControl(t, "full")
	q, hello := e.open(t, "full")
	if !helloCommands(hello)["pushDelay"] {
		t.Error("pushDelay is not in the hello of a device that may act")
	}
	q.send(command{Cmd: "pushDelay", Size: 120, Req: "d"})
	if got := q.readType("pushPrefs"); got["req"] != "d" || got["delaySeconds"] != float64(120) {
		t.Errorf("after pushDelay: %v", got)
	}
	q.send(command{Cmd: "pushNotify", Kind: "off", Req: "n"})
	if got := q.readType("pushPrefs"); got["off"] != true || got["delaySeconds"] != float64(120) {
		t.Errorf("after pushNotify: %v", got)
	}
	q.send(command{Cmd: "pushAnonymous", Kind: "on", Req: "a"})
	if got := q.readType("pushPrefs"); got["anonymous"] != true || got["off"] != true {
		t.Errorf("after pushAnonymous: %v", got)
	}
	// Out of bounds is not a change; the answer still says where it stands.
	for _, size := range []int{1, 4, 3601, -1, 1 << 40} {
		q.send(command{Cmd: "pushDelay", Size: size, Req: "b"})
		if got := q.readType("pushPrefs"); got["delaySeconds"] != float64(120) {
			t.Errorf("a delay of %d was taken: %v", size, got)
		}
	}
	// The default is kept as nothing, and read back as the default.
	q.send(command{Cmd: "pushDelay", Size: 30, Req: "z"})
	if got := q.readType("pushPrefs"); got["delaySeconds"] != float64(30) {
		t.Errorf("the default delay: %v", got)
	}
	if pp := e.srv.prefs.Push; !pp.Off || !pp.Anonymous || pp.DelaySeconds != 0 {
		t.Errorf("the desk's preferences = %+v", pp)
	}

	// The switch is looked at again for every request.
	e.setControl(t, "full", store.RoleFull, false, "")
	q.send(command{Cmd: "pushNotify", Kind: "on", Req: "late"})
	if r := q.readType("sealedRefused"); r["code"] != sealedCodeControl {
		t.Errorf("after the switch was taken away: %v", r)
	}
	if !e.srv.prefs.Push.Off {
		t.Error("a device without the switch turned notifications back on")
	}
}

// A viewer that may watch panes reads the preferences and cannot change them.
func TestSealedPushPrefsForAWatcher(t *testing.T) {
	e := newSealedEnv(t)
	p, _ := e.open(t, "watcher")
	if got := pushPrefsNow(t, p, "w"); got["req"] != "w" {
		t.Errorf("preferences = %v", got)
	}
	p.send(command{Cmd: "pushDelay", Size: 60, Req: "x"})
	if r := p.readType("sealedRefused"); r["code"] != sealedCodeControl {
		t.Errorf("a watcher's pushDelay was answered %v", r)
	}
}

// settingsRemote is the sealed test remote with a relay behind it: a roster that
// holds keys and another device, and a record of what was deleted.
type settingsRemote struct {
	*approvalsFake
	client *remote.Client
}

func (r *settingsRemote) Client() (*remote.Client, error) { return r.client, nil }

type settingsRelay struct {
	mu      sync.Mutex
	deletes []string
	fail    bool
}

func (r *settingsRelay) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.deletes...)
}

func newSettingsEnv(t *testing.T) (*sealedEnv, *settingsRelay) {
	t.Helper()
	e := newSealedEnv(t)
	rel := &settingsRelay{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete {
			rel.mu.Lock()
			rel.deletes = append(rel.deletes, req.URL.Path)
			fail := rel.fail
			rel.mu.Unlock()
			if fail {
				http.Error(w, "no "+settingsSecret, http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":[` +
			`{"id":"full","name":"My phone","created":"2026-01-02T03:04:05Z","lastSeen":"2026-11-16T10:00:00Z","publicKey":"PUBKEY-OF-FULL","deskPublicKey":"DESKKEY-OF-FULL"},` +
			`{"id":"other-device-id","name":"Old tablet","created":"2025-05-05T05:05:05Z","publicKey":"PUBKEY-OF-OTHER"}],` +
			`"hosts":[{"id":"host-secret-id","name":"Work desktop","self":true,"url":"https://host-secret.example"}],"plan":{"name":"plan-secret"}}`))
	}))
	t.Cleanup(srv.Close)
	e.srv.SetRemote(&settingsRemote{approvalsFake: e.fake, client: remote.NewClient(&remote.Config{Relay: srv.URL, Token: "fdh_test"}, "v")})
	return e, rel
}

// The list a phone is sent has names and times and which row it is. No key,
// fingerprint, host, plan or the id of another device.
func TestSealedDeviceRoster(t *testing.T) {
	e, _ := newSettingsEnv(t)
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "remoteDevices", Req: "d"})
	got := p.readType("deviceRoster")
	raw, _ := json.Marshal(got)
	for _, s := range []string{"PUBKEY", "DESKKEY", "other-device-id", "host-secret", "Work desktop", "plan-secret", "fingerprint", "publicKey", "verify", "role"} {
		if bytes.Contains(raw, []byte(s)) {
			t.Errorf("the roster holds %q: %s", s, raw)
		}
	}
	if got["self"] != "full" || got["enabled"] != true {
		t.Errorf("roster = %s", raw)
	}
	rows, _ := got["devices"].([]any)
	if len(rows) != 2 {
		t.Fatalf("devices = %v", got["devices"])
	}
	first, _ := rows[0].(map[string]any)
	second, _ := rows[1].(map[string]any)
	if first["name"] != "My phone" || first["current"] != true || second["name"] != "Old tablet" || second["current"] != nil {
		t.Errorf("rows = %v", rows)
	}
	if _, ok := first["id"]; ok {
		t.Error("a row has an id")
	}
}

// A viewer, even one let watch panes, is not sent the list.
func TestSealedDeviceRosterIsForFullDevices(t *testing.T) {
	e, _ := newSettingsEnv(t)
	p, _ := e.open(t, "watcher")
	p.send(command{Cmd: "remoteDevices", Req: "d"})
	if r := p.readType("sealedRefused"); r["code"] != sealedCodeRole {
		t.Errorf("a watcher's remoteDevices was answered %v", r)
	}
}

// With nothing enabled the answer is an empty list, not an error with a path.
func TestSealedDeviceRosterWhenRemoteIsOff(t *testing.T) {
	msg := sealedRoster(remoteDevicesMsg{Type: "remoteDevices", Error: "dial " + settingsSecret + "\nsecond line"}, "full")
	if msg.Devices == nil || len(msg.Devices) != 0 || strings.Contains(msg.Error, "\n") {
		t.Errorf("roster = %+v", msg)
	}
	out := sealedWrite(mustMarshal(msg), func() bool { return false })
	if out == nil || bytes.Contains(out, []byte(settingsSecret)) {
		t.Errorf("roster frame = %s", out)
	}
}

// A device unpairs itself, and only itself. The answer is written before the
// socket is closed, the relay is asked once, the desk is told.
func TestSealedSelfRevoke(t *testing.T) {
	e, rel := newSettingsEnv(t)
	p, _ := e.open(t, "full")

	// Another device's id, and none, are refused by name and the relay is not asked.
	for _, id := range []string{"other-device-id", "watcher", ""} {
		p.send(command{Cmd: "remoteRevoke", ID: id, Req: "o"})
		if r := p.readType("sealedRefused"); r["code"] != sealedCodeSelf || r["cmd"] != "remoteRevoke" {
			t.Errorf("remoteRevoke of %q was answered %v", id, r)
		}
	}
	if got := rel.seen(); len(got) != 0 {
		t.Fatalf("the relay was asked to unpair %v for another device", got)
	}

	p.send(command{Cmd: "remoteRevoke", ID: "full", Req: "me"})
	got := p.readType("deviceRevoked")
	if got["ok"] != true || got["req"] != "me" {
		t.Errorf("answer = %v", got)
	}
	sealedWaitFor(t, "the relay delete", func() bool { return len(rel.seen()) == 1 })
	if d := rel.seen(); !strings.HasSuffix(d[0], "/full") {
		t.Errorf("relay deletes = %v", d)
	}
	// The socket is ended by the desk.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := p.conn.Read(ctx); err == nil {
		t.Error("the socket is still open")
	}
}

// A viewer may leave too, without any switch: it only takes the device's own
// access away.
func TestSealedSelfRevokeForAWatcher(t *testing.T) {
	e, rel := newSettingsEnv(t)
	p, _ := e.open(t, "watcher")
	p.send(command{Cmd: "remoteRevoke", ID: "watcher", Req: "w"})
	if got := p.readType("deviceRevoked"); got["ok"] != true {
		t.Errorf("answer = %v", got)
	}
	sealedWaitFor(t, "the relay delete", func() bool { return len(rel.seen()) == 1 })
}

// When the relay refuses, the device is told in fixed words, stays paired and
// keeps its socket; nothing the relay said is passed on.
func TestSealedSelfRevokeFailure(t *testing.T) {
	e, rel := newSettingsEnv(t)
	rel.mu.Lock()
	rel.fail = true
	rel.mu.Unlock()
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "remoteRevoke", ID: "full", Req: "f"})
	got := p.readType("deviceRevoked")
	if got["ok"] != false || got["error"] != revokeSelfFailed {
		t.Errorf("answer = %v", got)
	}
	raw, _ := json.Marshal(got)
	if bytes.Contains(raw, []byte(settingsSecret)) {
		t.Errorf("the relay's words were passed on: %s", raw)
	}
	// Still served.
	if r := pushPrefsNow(t, p, "after"); r["req"] != "after" {
		t.Errorf("after a failure: %v", r)
	}
}

// Taken at the unsealed socket and by the other sealed paths, the self rule
// holds: the lift is for the sealed connection only.
func TestSealedSelfRevokeIsNotLiftedOnTheUnsealedSocket(t *testing.T) {
	sealed := &controlClient{remote: true, sealed: &sealedLink{}}
	plain := &controlClient{remote: true}
	for _, n := range []string{"remoteDevices", "pushPrefsGet", "ghPRs", "ghPRComment"} {
		if !sealed.liftsRelayRefusal(n) || plain.liftsRelayRefusal(n) {
			t.Errorf("%s: lifted on sealed %v, on unsealed %v", n, sealed.liftsRelayRefusal(n), plain.liftsRelayRefusal(n))
		}
	}
	for _, n := range []string{"remotePair", "remoteRename", "ghPRCreate", "ghLogin"} {
		if sealed.liftsRelayRefusal(n) {
			t.Errorf("%s is lifted on a sealed connection", n)
		}
	}
}

// ---- GitHub ----

func ghSealedEnv(t *testing.T) (*sealedEnv, string) {
	t.Helper()
	fakeGH(t)
	srv, _, repo := newRepoServer(t)
	return newSealedEnvFor(t, srv), repo
}

func TestSealedGitHubReadsAreForFullDevices(t *testing.T) {
	e, repo := ghSealedEnv(t)
	p, _ := e.open(t, "watcher")
	for _, n := range []string{"ghStatus", "ghPRs", "ghPR", "ghIssues", "ghIssue"} {
		p.send(command{Cmd: n, Path: repo, GHNumber: 1, Req: "r-" + n})
		if r := p.readType("sealedRefused"); r["code"] != sealedCodeRole || r["cmd"] != n {
			t.Errorf("a watcher's %s was answered %v", n, r)
		}
	}
}

// A full device with no switch reads; other people's text is cut and redacted.
func TestSealedGitHubReads(t *testing.T) {
	e, repo := ghSealedEnv(t)
	long := strings.Repeat("x", maxGHBody+500)
	ghListPRs = func(dir, state string, limit int) ([]ghcli.PR, error) {
		return []ghcli.PR{{Number: 7, Title: "fix " + settingsSecret, State: "OPEN"}}, nil
	}
	ghViewPR = func(dir string, n int) (*ghcli.PR, error) {
		cs := make([]ghcli.Comment, maxGHComments+10)
		for i := range cs {
			cs[i] = ghcli.Comment{Body: "c" + string(rune('A'+i%26))}
		}
		cs[len(cs)-1].Body = "last " + settingsSecret + strings.Repeat("y", maxGHComment+10)
		return &ghcli.PR{Number: n, Title: "t", Body: long, Comments: cs}, nil
	}
	ghViewIssue = func(dir string, n int) (*ghcli.Issue, error) {
		return &ghcli.Issue{Number: n, Title: "i", Body: "see " + settingsSecret}, nil
	}
	p, _ := e.open(t, "full")

	p.send(command{Cmd: "ghPRs", Path: repo, Req: "l"})
	list := p.readType("ghPRs")
	raw, _ := json.Marshal(list)
	if bytes.Contains(raw, []byte(settingsSecret)) || !strings.Contains(string(raw), `"number":7`) {
		t.Errorf("ghPRs = %s", raw)
	}

	p.send(command{Cmd: "ghPR", Path: repo, GHNumber: 7, Req: "v"})
	pr := p.readType("ghPR")
	item, _ := pr["item"].(map[string]any)
	if body, _ := item["body"].(string); len([]rune(body)) != maxGHBody {
		t.Errorf("the body is %d characters, want %d", len([]rune(body)), maxGHBody)
	}
	cs, _ := item["comments"].([]any)
	if len(cs) != maxGHComments {
		t.Errorf("%d comments, want the newest %d", len(cs), maxGHComments)
	}
	last, _ := cs[len(cs)-1].(map[string]any)
	if b, _ := last["body"].(string); len([]rune(b)) > maxGHComment || strings.Contains(b, settingsSecret) {
		t.Errorf("the last comment is %d characters: %.60q", len([]rune(b)), b)
	}

	p.send(command{Cmd: "ghIssue", Path: repo, GHNumber: 3, Req: "i"})
	is := p.readType("ghIssue")
	raw, _ = json.Marshal(is)
	if bytes.Contains(raw, []byte(settingsSecret)) {
		t.Errorf("ghIssue = %s", raw)
	}

	// The status of the sign-in is read; signing in or out is not offered.
	ghInstalled = func() bool { return true }
	ghGetAuthStatus = func(dir string) (ghcli.AuthStatus, error) {
		return ghcli.AuthStatus{LoggedIn: true, Account: "octo", Host: "github.com"}, nil
	}
	p.send(command{Cmd: "ghStatus", Path: repo, Req: "s"})
	if st := p.readType("ghStatus"); st["loggedIn"] != true || st["account"] != "octo" {
		t.Errorf("ghStatus = %v", st)
	}
}

// Creating, signing in and out, and installing are the desk's, whatever the
// device may do.
func TestSealedGitHubWritesBeyondCommentsAreRefused(t *testing.T) {
	e, repo := ghSealedEnv(t)
	e.grantControl(t, "full")
	created := false
	ghCreatePR = func(string, ghcli.PRCreateOptions) (*ghcli.PR, error) { created = true; return &ghcli.PR{}, nil }
	ghCreateIssue = func(string, ghcli.IssueCreateOptions) (*ghcli.Issue, error) { created = true; return &ghcli.Issue{}, nil }
	p, _ := e.open(t, "full")
	for _, n := range []string{"ghPRCreate", "ghIssueCreate", "ghLogin", "ghLogout", "ghInstall", "ghLoginCancel"} {
		p.send(command{Cmd: n, Path: repo, GHTitle: "t", Req: "r-" + n})
		if r := p.readType("sealedRefused"); r["code"] != sealedCodeCommand || r["cmd"] != n {
			t.Errorf("%s was answered %v", n, r)
		}
	}
	if created {
		t.Error("something was created on GitHub")
	}
}

func ghCommentEnv(t *testing.T) (*sealedEnv, *sealedPhone, *controlClient, string, *[]string) {
	t.Helper()
	fakeGH(t)
	srv, _, repo := newRepoServer(t)
	e := newSealedEnvFor(t, srv)
	e.grantControl(t, "full")
	desk := deskWindow(t, srv)
	var mu sync.Mutex
	var sent []string
	ghCommentPR = func(dir string, n int, body string) (*ghcli.Comment, error) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, "pr:"+body)
		return &ghcli.Comment{Body: body}, nil
	}
	ghCommentIssue = func(dir string, n int, body string) (*ghcli.Comment, error) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, "issue:"+body)
		return &ghcli.Comment{Body: body}, nil
	}
	ghViewPR = func(dir string, n int) (*ghcli.PR, error) { return &ghcli.PR{Number: n}, nil }
	ghViewIssue = func(dir string, n int) (*ghcli.Issue, error) { return &ghcli.Issue{Number: n}, nil }
	p, _ := e.open(t, "full")
	return e, p, desk, repo, &sent
}

// A comment waits for the desk, shows what it says and where it goes, and goes
// out only after a yes; the next one is asked again.
func TestSealedGitHubCommentWaitsForTheDesk(t *testing.T) {
	e, p, desk, repo, sent := ghCommentEnv(t)
	p.send(command{Cmd: "ghPRComment", Path: repo, GHNumber: 12, GHBody: "looks good to me", Req: "c1"})
	if w := p.gitState(confirmWaiting); w["cmd"] != "ghPRComment" || w["req"] != "c1" {
		t.Errorf("waiting = %v", w)
	}
	ask := nextDeskConfirm(t, desk)
	if ask.Action != "comment on a pull request" || !strings.Contains(ask.Summary, "pull request #12") || !strings.Contains(ask.Summary, "looks good to me") {
		t.Errorf("the desk is told %+v", ask)
	}
	time.Sleep(100 * time.Millisecond)
	if len(*sent) != 0 {
		t.Fatal("the comment went out before the desk answered")
	}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	p.gitState(confirmAllowed)
	if pr := p.readType("ghPR"); pr == nil {
		t.Fatal("no pull request after the comment")
	}
	if len(*sent) != 1 || (*sent)[0] != "pr:looks good to me" {
		t.Errorf("sent = %v", *sent)
	}

	// An issue is the same, and is asked again.
	p.send(command{Cmd: "ghIssueComment", Path: repo, GHNumber: 3, GHBody: "same here", Req: "c2"})
	p.gitState(confirmWaiting)
	ask2 := nextDeskConfirm(t, desk)
	for ask2.State != "ask" {
		ask2 = nextDeskConfirm(t, desk)
	}
	if ask2.ID == ask.ID || ask2.Action != "comment on an issue" || !strings.Contains(ask2.Summary, "issue #3") {
		t.Errorf("second ask = %+v", ask2)
	}
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask2.ID, Nonce: ask2.Nonce, On: false})
	p.gitState(confirmDenied)
	time.Sleep(100 * time.Millisecond)
	if len(*sent) != 1 {
		t.Errorf("a refused comment went out: %v", *sent)
	}
}

// A comment that is not well formed is not asked of the desk.
func TestSealedGitHubCommentBadRequests(t *testing.T) {
	_, p, desk, repo, sent := ghCommentEnv(t)
	for _, c := range []command{
		{Cmd: "ghPRComment", Path: repo, GHNumber: 0, GHBody: "x"},
		{Cmd: "ghPRComment", Path: repo, GHNumber: 1, GHBody: "  "},
		{Cmd: "ghIssueComment", Path: repo, GHNumber: 1, GHBody: strings.Repeat("a", confirmMaxMessage+1)},
		{Cmd: "ghIssueComment", GHNumber: 1, GHBody: "no folder"},
	} {
		p.send(c)
		p.gitState(confirmBad)
	}
	noDeskConfirm(t, desk)
	if len(*sent) != 0 {
		t.Errorf("sent = %v", *sent)
	}
}

// Without the switch a comment is refused by name and the desk is not asked; the
// unsealed socket refuses it whatever the role.
func TestSealedGitHubCommentNeedsControl(t *testing.T) {
	fakeGH(t)
	srv, _, repo := newRepoServer(t)
	e := newSealedEnvFor(t, srv)
	desk := deskWindow(t, srv)
	p, hello := e.open(t, "full")
	if helloCommands(hello)["ghPRComment"] {
		t.Error("a device without the switch is told it may comment")
	}
	p.send(command{Cmd: "ghPRComment", Path: repo, GHNumber: 1, GHBody: "x", Req: "c"})
	if r := p.readType("sealedRefused"); r["code"] != sealedCodeControl || r["cmd"] != "ghPRComment" {
		t.Errorf("answer = %v", r)
	}
	noDeskConfirm(t, desk)

	c := &controlClient{out: make(chan []byte, 8), remote: true, device: "full"}
	c.accessOf = func() store.DeviceAccess { return store.DeviceAccess{Role: store.RoleFull} }
	called := false
	ghCommentPR = func(string, int, string) (*ghcli.Comment, error) { called = true; return &ghcli.Comment{}, nil }
	for _, name := range []string{"ghPRComment", "ghIssueComment"} {
		srv.handleCommand(c, command{Cmd: name, Path: repo, GHNumber: 1, GHBody: "x"})
		var n noticeMsg
		if err := json.Unmarshal(<-c.out, &n); err != nil || n.Text != ghDeskOnly || !n.Error {
			t.Errorf("%s over the unsealed socket: %+v", name, n)
		}
	}
	noDeskConfirm(t, desk)
	if called {
		t.Error("a comment went out over the unsealed socket")
	}
}

// The log names the command and the outcome, never what a comment, a message or
// a preference said.
func TestSealedSettingsAuditHoldsNoText(t *testing.T) {
	e, p, desk, repo, _ := ghCommentEnv(t)
	const text = "audit-comment-text-must-not-appear"
	p.send(command{Cmd: "ghPRComment", Path: repo, GHNumber: 4, GHBody: text, Req: "a"})
	p.gitState(confirmWaiting)
	ask := nextDeskConfirm(t, desk)
	e.srv.handleCommand(desk, command{Cmd: "deskConfirmAnswer", ID: ask.ID, Nonce: ask.Nonce, On: true})
	p.gitState(confirmAllowed)
	time.Sleep(300 * time.Millisecond)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".log") {
			return nil
		}
		b, _ := os.ReadFile(path)
		if bytes.Contains(b, []byte(text)) {
			t.Errorf("%s holds the text of a comment", path)
		}
		return nil
	})
}
