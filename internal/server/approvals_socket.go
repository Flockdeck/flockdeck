package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/artifacts"
	"github.com/jmwri/flockdeck/internal/remote"
)

// /ws/approvals is the socket a paired phone is shown the permission requests of
// structured agents over, and answers them on (docs/phone-approval-plan.md,
// "The wire change" and "T3 as built"). It is modelled on /ws/artifacts and has
// the same two rules: it is for devices reached through the relay only (a request
// on the local listener is a 404), and end-to-end encryption is mandatory. No key
// on either side, a failed handshake or a missing hello closes the socket,
// because the relay is not to read a command or forge an answer.
//
// What a device must have before it is told anything, checked when the socket
// opens and again before every frame in either direction:
//
//   - the usual end-to-end key, on both sides, and a handshake that used it;
//   - the desk's mark that this key is verified;
//   - approval switched on for the device at the current wording, and granted for
//     this key (mayApproveWithKey, given the key the handshake proved and never one
//     read from the roster or sent by the device).
//
// Nothing the agent wrote reaches the phone except what projectCard lets
// through, and the phone answers allow or deny, never an option id: phoneAnswer
// maps it to the card's own option. A decision is made against an offer, a
// random value the host gave for the card it showed on this socket; it is used
// once, and it is not honoured sooner than approvalMinDisplay after the card was
// sent.

// What the socket says in a close, and the limits it keeps.
const (
	approvalReasonDisabled = "disabled" // approval is off for the device
	approvalReasonVerify   = "verify"   // the device's key is not marked verified
	approvalReasonKey      = "key"      // no end-to-end key on one side, or the handshake used another
	approvalReasonOrigin   = "origin"   // not the usual key origin
	approvalReasonGrant    = "grant"    // approval was allowed for a different key

	// approvalWireVersion is the version of the frames below, sent in hello.
	approvalWireVersion = 1
	// approvalMinDisplay is the least time between a card being sent and a
	// decision on it being taken, so that a tap meant for something that was on the
	// screen a moment ago does not answer a card that has just appeared under it.
	approvalMinDisplay = 700 * time.Millisecond
	// approvalMaxItems is how many waiting panes one frame lists.
	approvalMaxItems = 8
	// approvalMaxRequest is the most a request, once opened, may be.
	approvalMaxRequest = 4096
	// approvalPadBucket is the size every frame the host sends is rounded up to,
	// or the next multiple of it, so that the relay, which sees the size of each
	// sealed frame, does not learn how long a command is.
	approvalPadBucket = 1024
	// approvalAbuseMax is how many refusals of the kind a client that follows the
	// protocol never causes are put up with in a row. The next one closes the socket
	// and the desk is told.
	approvalAbuseMax = 3
	// approvalBusyMax is how many frames in a row may be turned away for rate
	// before the socket is closed.
	approvalBusyMax = 10
	// approvalSocketsPerDevice is how many sockets one device may hold.
	approvalSocketsPerDevice = 2
	// approvalConnectsPerMinute is how many sockets a device may open in a minute,
	// whether or not they get past the checks.
	approvalConnectsPerMinute = 20
	// approvalDecideBurst is how many decisions a device may make at once; after
	// that it is approvalDecideRate a second. These are starting values for the
	// live check (decision 7 of the plan).
	approvalDecideBurst = 5
	approvalDecideRate  = 1.0
	// approvalFrameBurst and approvalFrameRate limit every frame a device sends.
	approvalFrameBurst = 10
	approvalFrameRate  = 2.0
	// approvalOffersKept bounds the offers a socket remembers as used or ended.
	approvalOffersKept = 256
)

// The refusals a phone is told. Each is the whole of what it learns; none holds
// text from an agent. Those of phoneAnswer (errPhone...) and permissionStale are
// used as they are.
const (
	approvalErrBusy   = "Too many answers too fast. Wait a moment and try again"
	approvalErrOffer  = "That request is not one this connection was shown"
	approvalErrShape  = "The answer needs a pane, a card number, an offer and allow or deny"
	approvalErrMatch  = "That answer is not for the request that was offered"
	approvalErrEarly  = "That request was only just shown. Look at it, then answer"
	approvalErrRecord = "The answer could not be recorded, so it was not given"
	approvalErrGive   = "The answer could not be given"
)

// approvalAuditFile is the approvals audit log in the state folder.
const approvalAuditFile = "remote-approvals.log"

// approvalTimings are the time limits on a socket, read once when a socket is
// accepted, so that a test changes them for one server.
type approvalTimings struct {
	// idle ends a socket whose device has sent nothing for this long; first is
	// the same for a socket that has not sent its first frame.
	idle, first time.Duration
	// handshake bounds the end-to-end handshake; roster the lookups of the
	// device's key.
	handshake, roster time.Duration
	// poll is how often the host looks for a card that appeared, changed or
	// ended, once the device has asked for the list.
	poll time.Duration
	// minDisplay is approvalMinDisplay.
	minDisplay time.Duration
}

func (t approvalTimings) withDefaults() approvalTimings {
	if t.idle == 0 {
		t.idle = 5 * time.Minute
	}
	if t.first == 0 {
		t.first = time.Minute
	}
	if t.handshake == 0 {
		t.handshake = 20 * time.Second
	}
	if t.roster == 0 {
		t.roster = 10 * time.Second
	}
	if t.poll == 0 {
		t.poll = 500 * time.Millisecond
	}
	if t.minDisplay == 0 {
		t.minDisplay = approvalMinDisplay
	}
	return t
}

// approvalsState is what the server keeps about the approvals socket.
type approvalsState struct {
	mu      sync.Mutex
	open    map[string]int
	devices map[string]*approvalDevice
	denials map[string]*artifactDenial
	audit   auditLog
	timing  approvalTimings
}

// approvalDevice is what is shared by every socket of one device, so that
// opening several does not multiply its allowance.
type approvalDevice struct {
	decide, frames, connects *artifacts.Limiter
}

func (a *approvalsState) timings() approvalTimings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.timing.withDefaults()
}

func (a *approvalsState) setTimings(change func(*approvalTimings)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	change(&a.timing)
}

func (a *approvalsState) device(id string) *approvalDevice {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.devices[id]; d != nil {
		return d
	}
	if a.devices == nil || len(a.devices) >= 1024 {
		a.devices = map[string]*approvalDevice{}
	}
	dec, _ := artifacts.NewLimiter(approvalDecideRate, approvalDecideBurst, nil)
	fr, _ := artifacts.NewLimiter(approvalFrameRate, approvalFrameBurst, nil)
	cn, _ := artifacts.NewLimiter(float64(approvalConnectsPerMinute)/60, approvalConnectsPerMinute, nil)
	d := &approvalDevice{decide: dec, frames: fr, connects: cn}
	a.devices[id] = d
	return d
}

// take registers a socket of a device, or reports false if it holds the most it
// may. It is registered before the device is checked, so that a switch turned
// off between the two still finds it (through s.sockets).
func (a *approvalsState) take(device string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.open[device] >= approvalSocketsPerDevice {
		return false
	}
	if a.open == nil {
		a.open = map[string]int{}
	}
	a.open[device]++
	return true
}

func (a *approvalsState) release(device string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.open[device]--; a.open[device] <= 0 {
		delete(a.open, device)
	}
}

// denial says whether a refusal should be recorded and shown at the desk, and
// how many were left out since the last one recorded, as artifactState.denial.
func (a *approvalsState) denial(key string) (record, notify bool, suppressed int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	d := a.denials[key]
	if d == nil {
		if a.denials == nil || len(a.denials) >= artifactDenialsMax {
			a.denials = map[string]*artifactDenial{}
		}
		d = &artifactDenial{}
		a.denials[key] = d
	}
	if d.recorded.IsZero() || now.Sub(d.recorded) >= artifactDenialRecordEvery {
		record, suppressed = true, d.suppressed
		d.recorded, d.suppressed = now, 0
	} else {
		d.suppressed++
	}
	if d.notified.IsZero() || now.Sub(d.notified) >= artifactDenialNoticeEvery {
		notify = true
		d.notified = now
	}
	return record, notify, suppressed
}

// approvalDenied records a refusal in the approvals log and shows it at the
// desk, at a limited rate. A phone that follows the protocol is refused only when
// the desk has changed something, so a repeat is a sign of a modified client.
func (s *Server) approvalDenied(device, name, reason string) {
	record, notify, suppressed := s.approvals.denial(device + "\x00" + reason)
	if record {
		_ = s.approvals.audit.write(auditEvent{Event: "denied", Device: device, DeviceName: name, Reason: reason, Suppressed: suppressed})
	}
	if notify {
		who := tidy(name)
		if who == "" {
			who = "A paired device"
		}
		s.noticeDesk(who+" was refused phone approval ("+tidy(reason)+"). That is not something Flockdeck's own phone app does", true)
	}
}

// approvalCheck decides whether device may be shown cards and answer them now,
// from the running program's record and the preferences file. key is the device's
// usual end-to-end key as the handshake proved it. It fails closed.
func (s *Server) approvalCheck(device string, origin remote.KeyOrigin, key []byte) string {
	switch {
	case origin != approveKeyOrigin:
		return approvalReasonOrigin
	case !s.approveSwitchOn(device):
		return approvalReasonDisabled
	}
	v := s.deviceVerifier()
	if v == nil || len(key) == 0 || v.DeviceVerifyState(device, origin, key) != remote.Verified {
		return approvalReasonVerify
	}
	if !s.mayApproveWithKey(device, key) {
		return approvalReasonGrant
	}
	return ""
}

// approvalRequest is every field a request may use. Anything else, the padding
// the phone adds included, is not read.
type approvalRequest struct {
	Op       string `json:"op"`
	Pane     string `json:"pane"`
	Token    string `json:"token"`
	CardSeq  uint64 `json:"cardSeq"`
	Offer    string `json:"offer"`
	Decision string `json:"decision"`
}

// approvalItem is one waiting pane in an approvals frame. Offer is empty, and
// Answerable false, for a request the phone has nothing to answer; Why says so in
// fixed words. The agent's own request id is not sent: the host holds it against
// the offer.
type approvalItem struct {
	Pane       string `json:"pane"`
	Label      string `json:"label"`
	Project    string `json:"project"`
	Token      string `json:"token"`
	CardSeq    uint64 `json:"cardSeq"`
	Offer      string `json:"offer,omitempty"`
	Answerable bool   `json:"answerable"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Added      int    `json:"added"`
	Removed    int    `json:"removed"`
	CanAllow   bool   `json:"canAllow"`
	Why        string `json:"why,omitempty"`
	WaitedMS   int64  `json:"waitedMs"`
}

type approvalsFrame struct {
	Op    string         `json:"op"`
	V     int            `json:"v"`
	Items []approvalItem `json:"items"`
}

type approvalResultFrame struct {
	Op       string `json:"op"`
	V        int    `json:"v"`
	OK       bool   `json:"ok"`
	Pane     string `json:"pane,omitempty"`
	CardSeq  uint64 `json:"cardSeq,omitempty"`
	Decision string `json:"decision,omitempty"`
	Error    string `json:"error,omitempty"`
}

type approvalHelloFrame struct {
	Op           string `json:"op"`
	V            int    `json:"v"`
	Desk         string `json:"desk"`
	Device       string `json:"device"`
	MinDisplayMS int    `json:"minDisplayMs"`
	MaxItems     int    `json:"maxItems"`
	MaxRequest   int    `json:"maxRequest"`
	PadBucket    int    `json:"padBucket"`
}

type approvalErrorFrame struct {
	Op   string `json:"op"`
	V    int    `json:"v"`
	Code string `json:"code"`
}

type approvalPongFrame struct {
	Op string `json:"op"`
	V  int    `json:"v"`
}

// padded is the JSON of v, an object, with a "pad" field of zeros added so that
// its length is a multiple of approvalPadBucket. The phone strips it by ignoring
// the field; the host ignores the one the phone adds.
func padded(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) < 3 || b[0] != '{' || b[len(b)-1] != '}' {
		return nil, errors.New("a frame is a JSON object with a field")
	}
	const wrap = len(`,"pad":""`)
	n := len(b) + wrap
	target := (n + approvalPadBucket - 1) / approvalPadBucket * approvalPadBucket
	out := make([]byte, 0, target)
	out = append(out, b[:len(b)-1]...)
	out = append(out, `,"pad":"`...)
	out = append(out, bytes.Repeat([]byte{'0'}, target-n)...)
	out = append(out, `"}`...)
	return out, nil
}

// approvalWait is a waiting structured pane as the workspace goroutine saw it.
type approvalWait struct {
	pane, label, project string
	card                 *bridge.Card
	token, root          string
	own                  bool
	answer               StructuredPane
}

// approvalWaits lists the structured panes that have a permission request
// pending, in tab order, at most approvalMaxItems. It runs on the workspace
// goroutine. The pane is found through s.structuredPane, as permissionAnswer
// does, so no consumer asks what agent it is.
func (s *Server) approvalWaits() []approvalWait {
	if s.structuredPane == nil {
		return nil
	}
	var names map[string]string
	var out []approvalWait
	for _, t := range s.ws.Tabs {
		for _, id := range t.Tree.Panes() {
			p := s.ws.Pane(id)
			if p == nil {
				continue
			}
			sp, ok := s.structuredPane(id)
			if !ok || sp == nil {
				continue
			}
			card := sp.PendingPermission()
			if card == nil {
				continue
			}
			if names == nil {
				names = map[string]string{}
				for _, pr := range s.ws.Projects() {
					names[pr.Root] = pr.Name
				}
			}
			label := capName(p.Name)
			if label == "" {
				label = "Agent"
			}
			out = append(out, approvalWait{
				pane: id, label: label, project: capName(projectLabel(names, s.ws.RootOf(id))),
				card: card, token: sp.RuntimeToken(), root: sp.ProjectRoot(), own: sp.OwnAdapter(), answer: sp,
			})
			if len(out) >= approvalMaxItems {
				return out
			}
		}
	}
	return out
}

// approvalShown is a card this socket has told the device of, with the offer that
// makes it answerable. The lock holds the agent's request id, which never leaves
// the host.
type approvalShown struct {
	key, offer, pane, token string
	lock                    approvalLock
	item                    approvalItem
	shownAt                 time.Time
}

// approvalSession is one open socket's state. Only the goroutine that runs the
// socket uses it.
type approvalSession struct {
	s            *Server
	device, name string
	dev          *approvalDevice
	tm           approvalTimings
	tc           *e2eConn

	shown   map[string]*approvalShown // by pane
	byOffer map[string]*approvalShown
	used    map[string]struct{}
	retired map[string]struct{}
	// listed is whether the device has asked for the list; nothing is sent
	// unprompted before. lastSig is what was last sent, to tell a change.
	listed  bool
	lastSig string
	// abuse counts refusals of the kind a correct client never causes, in a row;
	// busy counts frames turned away for rate in a row.
	abuse, busy int
}

func (a *approvalSession) keep(m map[string]struct{}, offer string) {
	if len(m) >= approvalOffersKept {
		clear(m)
	}
	m[offer] = struct{}{}
}

// drop forgets a card's offer, remembering it as used or as ended.
func (a *approvalSession) drop(sh *approvalShown, used bool) {
	delete(a.byOffer, sh.offer)
	if a.shown[sh.pane] == sh {
		delete(a.shown, sh.pane)
	}
	if sh.offer == "" {
		return
	}
	if used {
		a.keep(a.used, sh.offer)
	} else {
		a.keep(a.retired, sh.offer)
	}
}

func textHash(s string) string {
	if s == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// refresh looks at what is waiting now, gives each card new to this socket an
// offer and a line in the log, forgets what has ended, and returns the items to
// send and whether they differ from what was last sent. A card that cannot be
// recorded is not shown: the error ends the socket.
func (a *approvalSession) refresh() (items []approvalItem, changed bool, err error) {
	waits, ok := ask(a.s, a.s.approvalWaits)
	if !ok {
		return nil, false, errors.New("the server is closing")
	}
	now := time.Now()
	live := map[string]bool{}
	var sig bytes.Buffer
	items = []approvalItem{}
	for _, w := range waits {
		pc := projectCard(w.card, w.own, w.root)
		lock, lockOK := lockFor(w.card, w.token)
		if !lockOK {
			pc = phoneCard{Why: whyDeskOnly}
		}
		key := w.pane + "|" + w.token + "|" + w.card.RequestID + "|" + strconv.FormatUint(w.card.Seq, 10)
		live[w.pane] = true
		sh := a.shown[w.pane]
		if sh != nil && sh.key != key {
			a.drop(sh, false)
			sh = nil
		}
		if sh == nil {
			sh = &approvalShown{key: key, pane: w.pane, token: w.token, lock: lock, shownAt: now}
			kind := pc.Kind
			if kind == "" {
				kind = phoneOther
			}
			sh.item = approvalItem{
				Pane: w.pane, Label: w.label, Project: w.project, Token: w.token, CardSeq: w.card.Seq,
				Answerable: pc.Answerable, Kind: kind, Text: pc.Text, Added: pc.Added, Removed: pc.Removed,
				CanAllow: pc.CanAllow, Why: pc.Why,
			}
			if pc.Answerable && lockOK {
				offer, oerr := randomHex(16)
				if oerr != nil {
					return nil, false, oerr
				}
				sh.offer, sh.item.Offer = offer, offer
			}
			reason := ""
			switch {
			case !pc.Answerable:
				reason = "desk-only"
			case !pc.CanAllow:
				reason = "deny-only"
			}
			if werr := a.s.approvals.audit.write(auditEvent{
				Event: "shown", Device: a.device, DeviceName: a.name, Pane: w.pane, Name: w.label, Kind: kind,
				Seq: w.card.Seq, Len: len(pc.Text), Hash: textHash(pc.Text), Reason: reason,
			}); werr != nil {
				return nil, false, werr
			}
			a.shown[w.pane] = sh
			if sh.offer != "" {
				a.byOffer[sh.offer] = sh
			}
		}
		it := sh.item
		it.WaitedMS = now.Sub(sh.shownAt).Milliseconds()
		items = append(items, it)
		sig.WriteString(key + "|" + sh.offer + ";")
	}
	for pane, sh := range a.shown {
		if !live[pane] {
			a.drop(sh, false)
		}
	}
	s := sig.String()
	changed = s != a.lastSig
	a.lastSig = s
	return items, changed, nil
}

// send seals one frame, padded, and writes it within the write bound.
func (a *approvalSession) send(ctx context.Context, v any) bool {
	data, err := padded(v)
	if err != nil {
		return false
	}
	wctx, cancel := context.WithTimeout(ctx, artifactWriteTimeout)
	defer cancel()
	return a.tc.Write(wctx, websocket.MessageText, data) == nil
}

// sendList sends the approvals frame, if force or the set changed. It reports
// false when the socket should end: the device may no longer be told, a card
// could not be recorded, or the write failed.
func (a *approvalSession) sendList(ctx context.Context, force bool) (ok bool, end string) {
	items, changed, err := a.refresh()
	if err != nil {
		return false, "unavailable"
	}
	if !force && !changed {
		return true, ""
	}
	return a.send(ctx, approvalsFrame{Op: "approvals", V: approvalWireVersion, Items: items}), ""
}

// decide handles an approvalDecide request. It returns the reply, and a reason
// to end the socket, empty if it goes on.
func (a *approvalSession) decide(req approvalRequest) (res approvalResultFrame, end string) {
	s := a.s
	res = approvalResultFrame{Op: "approvalResult", V: approvalWireVersion, Pane: tidy(req.Pane), CardSeq: req.CardSeq}
	var seq uint64
	var paneID, kind, text string
	no := func(msg, reason string, abuse bool) (approvalResultFrame, string) {
		res.Error = msg
		_ = s.approvals.audit.write(auditEvent{Event: "decision-refused", Device: a.device, DeviceName: a.name, Pane: paneID, Seq: seq, Kind: kind, Reason: reason})
		if !abuse {
			return res, ""
		}
		if a.abuse++; a.abuse >= approvalAbuseMax {
			s.approvalDenied(a.device, a.name, "refused-answers")
			return res, "abuse"
		}
		return res, ""
	}

	if !a.dev.decide.Allow(1) {
		if a.busy++; a.busy > approvalBusyMax {
			s.approvalDenied(a.device, a.name, "rate")
			return res, artifactErrBusy
		}
		return no(approvalErrBusy, "rate", false)
	}
	if req.Pane == "" || req.Offer == "" || req.CardSeq == 0 || req.Token == "" {
		return no(approvalErrShape, "shape", true)
	}
	if req.Decision != decisionAllow && req.Decision != decisionDeny {
		return no(errPhoneDecision.Error(), "decision", true)
	}
	sh := a.byOffer[req.Offer]
	if sh == nil {
		if _, gone := a.retired[req.Offer]; gone {
			return no(permissionStale, "ended", false)
		}
		// Unknown, or used: a client that follows the protocol sends neither.
		return no(approvalErrOffer, "offer", true)
	}
	// The offer is used by this attempt, whatever it comes to: a guess at the
	// other fields costs the offer, and a refused tap has the card offered anew.
	a.drop(sh, true)
	paneID, seq, kind, text = sh.pane, sh.lock.Seq, sh.item.Kind, sh.item.Text
	if req.Pane != sh.pane || req.CardSeq != sh.lock.Seq || subtle.ConstantTimeCompare([]byte(req.Token), []byte(sh.token)) != 1 {
		return no(approvalErrMatch, "mismatch", true)
	}
	if req.Decision == decisionAllow && !sh.item.CanAllow {
		return no(errPhoneNoAllow.Error(), "allow-not-offered", true)
	}
	if time.Since(sh.shownAt) < a.tm.minDisplay {
		return no(approvalErrEarly, "too-soon", false)
	}

	type paneNow struct {
		card  *bridge.Card
		token string
		root  string
		own   bool
		pane  StructuredPane
	}
	now, ok := ask(s, func() paneNow {
		if s.structuredPane == nil {
			return paneNow{}
		}
		p, found := s.structuredPane(sh.pane)
		if !found || p == nil {
			return paneNow{}
		}
		return paneNow{card: p.PendingPermission(), token: p.RuntimeToken(), root: p.ProjectRoot(), own: p.OwnAdapter(), pane: p}
	})
	if !ok {
		return res, "closing"
	}
	if now.pane == nil {
		return no(errPhoneNoCard.Error(), "no-pane", false)
	}
	id, err := phoneAnswer(now.card, now.token, now.own, now.root, sh.lock, req.Decision)
	if err != nil {
		reason := "refused"
		switch {
		case errors.Is(err, errPhoneStale):
			reason = "stale"
		case errors.Is(err, errPhoneStaleRun):
			reason = "restarted"
		case errors.Is(err, errPhoneNoCard):
			reason = "no-card"
		}
		return no(err.Error(), reason, false)
	}
	// Seq is the card's number and unique, but what is shown is checked as well:
	// the card must read now as it did when it was sent.
	if p := projectCard(now.card, now.own, now.root); p.Text != text || p.Kind != sh.item.Kind {
		return no(permissionStale, "changed", false)
	}
	decision := "reject_once"
	if req.Decision == decisionAllow {
		decision = "allow_once"
	}
	// An answer with no trace is not given: the line is written first.
	if werr := s.approvals.audit.write(auditEvent{
		Event: "decision", Device: a.device, DeviceName: a.name, Pane: paneID, Name: sh.item.Label, Seq: seq,
		Decision: decision, Kind: kind, Len: len(text), Hash: textHash(text),
	}); werr != nil {
		return no(approvalErrRecord, "audit", false)
	}
	if gerr := now.pane.AnswerPermission(sh.lock.Seq, sh.lock.RequestID, id); gerr != nil {
		if errors.Is(gerr, ErrPermissionStale) {
			return no(permissionStale, "stale", false)
		}
		return no(approvalErrGive, "error", false)
	}
	permissionLog("permission answered from a phone: pane %s card %d (%s)", cleanForNotice(paneID), seq, decision)
	a.abuse, a.busy = 0, 0
	res.OK, res.Decision = true, req.Decision
	return res, ""
}

// handleApprovals serves /ws/approvals.
func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	// Only through the relay. Loopback cannot forge the mark, and the desk has
	// no use for this socket.
	if !fromRemote(r) {
		http.NotFound(w, r)
		return
	}
	device := r.Header.Get("Flockdeck-Remote-Device")
	// A browser always sends Origin on a WebSocket; one without is not a page.
	if device == "" || r.Header.Get("Origin") == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := tidy(r.Header.Get("Flockdeck-Remote-Device-Name"))
	origin := remote.KeyOriginUsual
	if r.Header.Get("Flockdeck-Remote-Origin") == "desk" {
		origin = remote.KeyOriginDesk
	}

	// No origin patterns and no compression, as /ws/artifacts: this is ciphertext.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(approvalMaxRequest + 1024)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-s.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	refuse := func(code int, reason string) {
		_ = conn.Close(websocket.StatusCode(code), reason)
	}
	tm := s.approvals.timings()
	dev := s.approvals.device(device)
	if !dev.connects.Allow(1) {
		s.approvalDenied(device, name, "connect-rate")
		refuse(artifactCloseBusy, artifactErrBusy)
		return
	}
	if !s.approvals.take(device) {
		s.approvalDenied(device, name, artifactErrBusy)
		refuse(artifactCloseBusy, artifactErrBusy)
		return
	}
	defer s.approvals.release(device)
	// Registered before it is checked, so that a switch turned off between the
	// two ends it (s.sockets.cut, called when approval is switched off).
	forget := s.sockets.add(device, func() {
		go func() { _ = conn.Close(websocket.StatusCode(artifactCloseRefused), approvalReasonDisabled) }()
	})
	defer forget()

	// Encryption is not optional: both sides must have a key on file.
	ra := s.remoteAccess()
	pctx, pcancel := context.WithTimeout(ctx, tm.roster)
	defer pcancel()
	if ra == nil || !ra.E2ECapable(pctx, device, origin) {
		s.approvalDenied(device, name, approvalReasonKey)
		refuse(artifactCloseRefused, approvalReasonKey)
		return
	}
	key, ok := s.deviceKey(pctx, device, origin)
	if !ok {
		s.approvalDenied(device, name, approvalReasonKey)
		refuse(artifactCloseRefused, approvalReasonKey)
		return
	}
	if reason := s.approvalCheck(device, origin, key); reason != "" {
		s.approvalDenied(device, name, reason)
		refuse(artifactCloseRefused, reason)
		return
	}
	hctx, hcancel := context.WithTimeout(ctx, tm.handshake)
	sess, err := s.e2eHandshake(hctx, ra, conn, device, origin)
	hcancel()
	if err != nil {
		// A failed handshake is a fault or a downgrade attempt, never a reason to
		// go on unencrypted.
		s.approvalDenied(device, name, "handshake")
		refuse(int(websocket.StatusPolicyViolation), "end-to-end handshake failed")
		return
	}
	// The key the handshake used is the key that was checked, and the one the
	// grant is held to: asked again, since the roster the handshake read is cached
	// for a few seconds and a key replaced in between would otherwise be served on
	// the strength of the old one's verification.
	rctx, rcancel := context.WithTimeout(ctx, tm.roster)
	defer rcancel()
	if again, ok := s.deviceKey(rctx, device, origin); !ok || !bytes.Equal(again, key) {
		s.approvalDenied(device, name, approvalReasonVerify)
		refuse(artifactCloseRefused, approvalReasonVerify)
		return
	}
	if reason := s.approvalCheck(device, origin, key); reason != "" {
		s.approvalDenied(device, name, reason)
		refuse(artifactCloseRefused, reason)
		return
	}
	// Opening is recorded before anything is served; if it cannot be, nothing is.
	if err := s.approvals.audit.write(auditEvent{Event: "connect", Device: device, DeviceName: name}); err != nil {
		refuse(int(websocket.StatusInternalError), "unavailable")
		return
	}

	a := &approvalSession{
		s: s, device: device, name: name, dev: dev, tm: tm, tc: &e2eConn{conn: conn, sess: sess},
		shown: map[string]*approvalShown{}, byOffer: map[string]*approvalShown{},
		used: map[string]struct{}{}, retired: map[string]struct{}{},
	}
	type frameIn struct {
		typ  websocket.MessageType
		data []byte
	}
	frames := make(chan frameIn)
	go func() {
		defer close(frames)
		wait := tm.first
		for {
			rc, rcl := context.WithTimeout(ctx, wait)
			wait = tm.idle
			typ, data, err := a.tc.Read(rc)
			rcl()
			if err != nil {
				return
			}
			select {
			case frames <- frameIn{typ, data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	gate := func() bool {
		if reason := s.approvalCheck(device, origin, key); reason != "" {
			s.approvalDenied(device, name, reason)
			refuse(artifactCloseRefused, reason)
			return false
		}
		return true
	}
	endWith := func(reason string) {
		switch reason {
		case "unavailable":
			refuse(int(websocket.StatusInternalError), "unavailable")
		case artifactErrBusy:
			refuse(artifactCloseBusy, artifactErrBusy)
		case "abuse":
			refuse(artifactCloseRefused, "refused")
		}
	}
	tick := time.NewTicker(tm.poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			// Nothing is sent until the device has asked, and nothing at all once
			// it may not be told.
			if !a.listed {
				continue
			}
			if !gate() {
				return
			}
			if ok, end := a.sendList(ctx, false); !ok {
				endWith(end)
				return
			}
		case f, open := <-frames:
			if !open {
				return
			}
			// The gate is checked again before every frame is acted on, from the
			// preferences on disk, so a switch turned off takes effect on the next.
			if !gate() {
				return
			}
			if !a.frame(ctx, f.typ, f.data, endWith) {
				return
			}
		}
	}
}

// frame handles one request from the device and reports whether the socket goes
// on.
func (a *approvalSession) frame(ctx context.Context, typ websocket.MessageType, data []byte, endWith func(string)) bool {
	s := a.s
	if !a.dev.frames.Allow(1) {
		if a.busy++; a.busy > approvalBusyMax {
			s.approvalDenied(a.device, a.name, "rate")
			endWith(artifactErrBusy)
			return false
		}
		return a.send(ctx, approvalErrorFrame{Op: "error", V: approvalWireVersion, Code: artifactErrBusy})
	}
	var req approvalRequest
	if typ != websocket.MessageText || len(data) > approvalMaxRequest || json.Unmarshal(data, &req) != nil {
		// Never sent by a client that follows the protocol.
		if a.abuse++; a.abuse >= approvalAbuseMax {
			s.approvalDenied(a.device, a.name, "malformed")
			endWith("abuse")
			return false
		}
		return a.send(ctx, approvalErrorFrame{Op: "error", V: approvalWireVersion, Code: artifactErrUnavailable})
	}
	switch req.Op {
	case "hello":
		return a.send(ctx, approvalHelloFrame{
			Op: "hello", V: approvalWireVersion, Desk: s.deskName(), Device: a.name,
			MinDisplayMS: int(a.tm.minDisplay / time.Millisecond), MaxItems: approvalMaxItems,
			MaxRequest: approvalMaxRequest, PadBucket: approvalPadBucket,
		})
	case "ping":
		return a.send(ctx, approvalPongFrame{Op: "pong", V: approvalWireVersion})
	case "approvalList":
		a.listed = true
		ok, end := a.sendList(ctx, true)
		if !ok {
			endWith(end)
		}
		return ok
	case "approvalDecide":
		res, end := a.decide(req)
		if end != "" {
			if end != "closing" {
				// Say why before closing; the answer to a refused request is a refusal.
				a.send(ctx, res)
				endWith(end)
			}
			return false
		}
		if !a.send(ctx, res) {
			return false
		}
		// The card is gone, or has a new offer: tell the device now.
		a.listed = true
		ok, end := a.sendList(ctx, true)
		if !ok {
			endWith(end)
		}
		return ok
	}
	// An op this host does not know is ignored, so a newer client can ask for
	// more than an older host offers.
	return true
}
