package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/artifacts"
	"github.com/jmwri/flockdeck/internal/remote"
)

// /ws/artifacts is the socket a paired device reads artifacts over. It is the
// one place in the server where end-to-end encryption is mandatory: the
// terminal falls back to plaintext for a browser that has no key, and this
// does not. No key on either side, a failed handshake or a missing hello
// closes the socket, because a relay that could strip the handshake could
// then read every listing and every byte.
//
// It is for devices reached through the relay only. A request on the local
// listener is a 404; the desk uses its own means. The client never names a
// path, URL or port: the request has no field for one, and what is served later
// is named by an id this host issued on this socket (artifacts.Registry).
//
// For now the socket serves nothing: every list is empty. What is here is the
// gate, the encryption, the limits, the audit trail and the kill switches, so
// that serving content later only adds content.

// Close codes the socket ends with. 4403 is a refusal, with one of the
// artifactReason strings; the rest say why a socket that was fine was ended.
const (
	artifactCloseRefused  = 4403
	artifactCloseBusy     = 4429
	artifactCloseProtocol = 4400
)

// artifactIdleTimeout ends a socket that has said nothing for this long. A
// variable so a test does not wait it out.
var artifactIdleTimeout = 5 * time.Minute

// artifactWriteTimeout bounds one write to a device that has stopped reading.
const artifactWriteTimeout = 10 * time.Second

// artifactRequestTimeout is the most one request may take from the moment it is
// read to the moment its reply is written. Whatever a request does, including
// anything it asks of the disk later, runs under a context that ends then, and
// the device is told "timeout". A variable so a test does not wait it out.
var artifactRequestTimeout = 30 * time.Second

// artifactHandshakeTimeout bounds the whole of opening a socket, from the
// upgrade to the first request, so that a device cannot hold one of its three
// slots with a handshake it never finishes.
var artifactHandshakeTimeout = 20 * time.Second

// artifactConnectsPerMinute is how many sockets a device may open in a minute,
// whether or not they get past the checks. The request limit starts only once a
// socket is open, so without this a device could open and drop sockets, each
// one costing a look at the relay's roster and a handshake, as fast as it liked.
const artifactConnectsPerMinute = 20

// artifactRequestHook, when set by a test, is called with the context a request
// runs under, before the request is answered.
var artifactRequestHook func(ctx context.Context, op string)

// artifactUnknownMax is how many requests for an id this socket never issued
// are put up with in a minute. A client that follows the protocol never sends
// one, so the socket is closed on the next and the desk is told.
const artifactUnknownMax = 5

// artifactBusyMax is how many requests in a row may be turned away for rate
// before the socket is closed.
const artifactBusyMax = 20

// artifactState is everything the server keeps about artifacts apart from the
// preferences, which are on disk.
type artifactState struct {
	mu       sync.Mutex
	verifier ArtifactVerifier
	socks    map[*artifactSock]struct{}
	devices  map[string]*artifactDevice
	// announced is the devices the desk has been told about this run.
	announced map[string]bool
	denials   map[string]*artifactDenial
	audit     auditLog
}

// artifactDevice is what is shared by every socket of one device, so that
// opening several does not multiply its allowance.
type artifactDevice struct {
	reqs  *artifacts.Limiter
	bytes *artifacts.Limiter
	// connects limits how often a socket is opened at all.
	connects *artifacts.Limiter
}

// artifactSock is one open socket.
type artifactSock struct {
	device string
	conn   *websocket.Conn
	reg    *artifacts.Registry
}

// artifactDenial tracks how often one refusal has been recorded and shown.
type artifactDenial struct {
	recorded, notified time.Time
	suppressed         int
}

// Refusals are recorded at most this often per device and reason, and shown at
// the desk at most this often.
const (
	artifactDenialRecordEvery = 10 * time.Second
	artifactDenialNoticeEvery = time.Minute
	artifactDenialsMax        = 256
)

// denial says whether a refusal should be recorded and whether it should be
// shown at the desk, and how many were left out since the last one recorded.
func (a *artifactState) denial(key string) (record, notify bool, suppressed int) {
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

// device returns the shared allowances of a device, made on first use.
func (a *artifactState) device(id string) *artifactDevice {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d := a.devices[id]; d != nil {
		return d
	}
	if a.devices == nil || len(a.devices) >= 1024 {
		// Device ids come from the relay and there are few; the bound is only a
		// guard. Starting over gives a device a fresh allowance, but only at
		// that scale, which needs a thousand paired devices to reach.
		a.devices = map[string]*artifactDevice{}
	}
	reqs, _ := artifacts.NewLimiter(float64(artifacts.RequestsPerWindow)/artifacts.RequestWindow.Seconds(), artifacts.RequestsPerWindow, nil)
	by, _ := artifacts.NewLimiter(float64(artifacts.BytesPerMinute)/60, artifacts.BytesPerMinute, nil)
	conns, _ := artifacts.NewLimiter(float64(artifactConnectsPerMinute)/60, artifactConnectsPerMinute, nil)
	d := &artifactDevice{reqs: reqs, bytes: by, connects: conns}
	a.devices[id] = d
	return d
}

// open registers a socket, or reports false if the device already holds the
// most it may. It is registered before it is checked, so that a switch turned
// off between the two still finds it.
func (a *artifactState) open(sock *artifactSock) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for s := range a.socks {
		if s.device == sock.device {
			n++
		}
	}
	if n >= artifacts.MaxSocketsPerDevice {
		return false
	}
	if a.socks == nil {
		a.socks = map[*artifactSock]struct{}{}
	}
	a.socks[sock] = struct{}{}
	return true
}

func (a *artifactState) closed(sock *artifactSock) {
	a.mu.Lock()
	delete(a.socks, sock)
	a.mu.Unlock()
	sock.reg.Clear()
}

// closeAll ends every socket and forgets every id, and says how many sockets
// there were.
func (a *artifactState) closeAll(why string) int {
	return a.closeMatching(why, func(*artifactSock) bool { return true })
}

// closeDevice ends every socket of one device.
func (a *artifactState) closeDevice(device, why string) int {
	return a.closeMatching(why, func(s *artifactSock) bool { return s.device == device })
}

func (a *artifactState) closeMatching(why string, match func(*artifactSock) bool) int {
	a.mu.Lock()
	var hit []*artifactSock
	for s := range a.socks {
		if match(s) {
			hit = append(hit, s)
		}
	}
	a.mu.Unlock()
	for _, s := range hit {
		s.reg.Clear()
		s.end(why)
	}
	return len(hit)
}

// end closes the socket with a refusal and a reason. It does not wait for the
// device to answer the close, so it is safe to call from anywhere; a device
// that never does is dropped when the close times out.
func (s *artifactSock) end(why string) {
	go func() { _ = s.conn.Close(websocket.StatusCode(artifactCloseRefused), why) }()
}

// announce says whether this is the first time the desk hears of a device this
// run, and remembers it.
func (a *artifactState) announce(device string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.announced[device] {
		return false
	}
	if a.announced == nil {
		a.announced = map[string]bool{}
	}
	a.announced[device] = true
	return true
}

// artifactRequest is every field a request may use. There is deliberately no
// path, URL, port or cwd: a client that sends one is not heard, because
// nothing reads it.
type artifactRequest struct {
	Op    string `json:"op"`
	V     int    `json:"v"`
	Kind  string `json:"kind"`
	Pane  string `json:"pane"`
	After string `json:"after"`
	ID    string `json:"id"`
}

// The only errors a device is told, and with no detail beyond the code.
const (
	artifactErrUnavailable = "unavailable"
	artifactErrBusy        = "busy"
	artifactErrDisabled    = "disabled"
	artifactErrTooLarge    = "too_large"
	artifactErrTimeout     = "timeout"
)

// artifactLimits is what hello says the limits are.
type artifactLimits struct {
	Chunk     int `json:"chunk"`
	Request   int `json:"request"`
	ListItems int `json:"listItems"`
}

// handleArtifacts serves /ws/artifacts.
func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	// Only through the relay. Loopback cannot forge the mark, and the desk has
	// no use for this socket.
	if !fromRemote(r) {
		http.NotFound(w, r)
		return
	}
	device := r.Header.Get("Flockdeck-Remote-Device")
	// A browser always sends Origin on a WebSocket, so a request without one is
	// not a page, and the one thing that stops another site's page opening this
	// socket is checked against it.
	if device == "" || r.Header.Get("Origin") == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := tidy(r.Header.Get("Flockdeck-Remote-Device-Name"))
	origin := remote.KeyOriginUsual
	if r.Header.Get("Flockdeck-Remote-Origin") == "desk" {
		origin = remote.KeyOriginDesk
	}

	// No origin patterns, as the other sockets: the page must have come from
	// this server's own address. No compression: this is ciphertext.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// Large enough for a sealed request of the largest size, and no larger.
	conn.SetReadLimit(artifacts.MaxRequestBytes + 1024)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sock := &artifactSock{device: device, conn: conn, reg: artifacts.NewRegistry(nil, 0, 0)}
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
	dev := s.artifacts.device(device)
	if !dev.connects.Allow(1) {
		s.artifactDenied(device, name, "connect-rate")
		refuse(artifactCloseBusy, artifactErrBusy)
		return
	}
	if !s.artifacts.open(sock) {
		s.artifactDenied(device, name, artifactErrBusy)
		refuse(artifactCloseBusy, artifactErrBusy)
		return
	}
	defer s.artifacts.closed(sock)

	// Encryption is not optional. Both sides must have a key on file, and the
	// device's must be the one the user verified.
	ra := s.remoteAccess()
	if ra == nil || !ra.E2ECapable(ctx, device, origin) {
		s.artifactDenied(device, name, artifactReasonKey)
		refuse(artifactCloseRefused, artifactReasonKey)
		return
	}
	key, ok := s.deviceKey(ctx, device, origin)
	if !ok {
		s.artifactDenied(device, name, artifactReasonKey)
		refuse(artifactCloseRefused, artifactReasonKey)
		return
	}
	kinds, reason := s.artifactCheck(device, origin, key)
	if reason != "" {
		s.artifactDenied(device, name, reason)
		refuse(artifactCloseRefused, reason)
		return
	}
	hctx, hcancel := context.WithTimeout(ctx, artifactHandshakeTimeout)
	sess, err := s.e2eHandshake(hctx, ra, conn, device, origin)
	hcancel()
	if err != nil {
		// A failed handshake is a fault or a downgrade attempt, never a reason
		// to go on unencrypted.
		s.artifactDenied(device, name, "handshake")
		refuse(int(websocket.StatusPolicyViolation), "end-to-end handshake failed")
		return
	}
	// The key the handshake used must be the key that was checked. The roster
	// the handshake read is cached for a few seconds, so a key replaced in
	// between would otherwise be served on the strength of the old one's
	// verification.
	if again, ok := s.deviceKey(ctx, device, origin); !ok || !bytes.Equal(again, key) {
		s.artifactDenied(device, name, artifactReasonVerify)
		refuse(artifactCloseRefused, artifactReasonVerify)
		return
	}
	tc := &e2eConn{conn: conn, sess: sess}

	// Opening is recorded before anything is served; if it cannot be recorded,
	// nothing is.
	if err := s.artifacts.audit.write(auditEvent{Event: "connect", Device: device, DeviceName: name}); err != nil {
		refuse(int(websocket.StatusInternalError), "unavailable")
		return
	}
	if s.artifacts.announce(device) {
		who := name
		if who == "" {
			who = "A paired device"
		}
		s.noticeDesk(who+" opened remote artifacts on this machine. Stop all remote viewing now closes it", false)
	}

	var busyRun int
	var unknown []time.Time
	for {
		rctx, rcancel := context.WithTimeout(ctx, artifactIdleTimeout)
		typ, data, err := tc.Read(rctx)
		rcancel()
		if err != nil {
			return
		}
		// Everything from here to the reply is bounded by one deadline.
		qctx, qcancel := context.WithTimeout(ctx, artifactRequestTimeout)
		more := func() bool {
			// The check is made again on every request, from the preferences on
			// disk, so a switch turned off takes effect on the next frame.
			var reason string
			kinds, reason = s.artifactCheck(device, origin, key)
			if reason != "" {
				s.artifactDenied(device, name, reason)
				refuse(artifactCloseRefused, reason)
				return false
			}
			if !dev.reqs.Allow(1) {
				if busyRun++; busyRun > artifactBusyMax {
					s.artifactDenied(device, name, "rate")
					refuse(artifactCloseBusy, artifactErrBusy)
					return false
				}
				return s.artifactReply(qctx, tc, dev, map[string]any{"op": "error", "code": artifactErrBusy})
			}
			busyRun = 0

			if typ != websocket.MessageText {
				return s.artifactReply(qctx, tc, dev, map[string]any{"op": "error", "code": artifactErrUnavailable})
			}
			if len(data) > artifacts.MaxRequestBytes {
				return s.artifactReply(qctx, tc, dev, map[string]any{"op": "error", "code": artifactErrTooLarge})
			}
			var req artifactRequest
			if json.Unmarshal(data, &req) != nil {
				return s.artifactReply(qctx, tc, dev, map[string]any{"op": "error", "code": artifactErrUnavailable})
			}
			if artifactRequestHook != nil {
				artifactRequestHook(qctx, req.Op)
			}

			var reply map[string]any
			switch req.Op {
			case "hello":
				reply = map[string]any{
					"op": "hello", "v": 1,
					"kinds": artifactKindsJSON(kinds),
					"limits": artifactLimits{
						Chunk: artifacts.ChunkBytes, Request: artifacts.MaxRequestBytes, ListItems: artifacts.MaxListItems,
					},
				}
			case "list":
				reply = s.artifactList(req, kinds)
			case "open":
				// Nothing is listed yet, so no id was ever issued on this socket and
				// every id is unknown. The registry is asked anyway: it is what later
				// lookups go through.
				if _, err := sock.reg.Lookup(req.ID, req.Kind, req.Pane); err != nil {
					now := time.Now()
					unknown = append(pruneBefore(unknown, now.Add(-time.Minute)), now)
					if len(unknown) > artifactUnknownMax {
						s.artifactDenied(device, name, "unknown-ids")
						refuse(artifactCloseRefused, "unknown ids")
						return false
					}
				}
				reply = map[string]any{"op": "error", "code": artifactErrUnavailable}
			case "close":
				reply = map[string]any{"op": "close"}
			default:
				// An op this host does not know is ignored, so a newer client can
				// ask for more than an older host offers.
				return true
			}
			// A request that ran out of time is answered with that, on the
			// socket's own clock, since the request's has gone.
			if qctx.Err() != nil {
				return s.artifactReply(ctx, tc, dev, map[string]any{"op": "error", "code": artifactErrTimeout})
			}
			return s.artifactReply(qctx, tc, dev, reply)
		}()
		qcancel()
		if !more {
			return
		}
	}
}

// artifactList answers a list request. It is empty for every kind for now;
// what it already decides is which kinds may be asked for.
func (s *Server) artifactList(req artifactRequest, kinds []string) map[string]any {
	for _, k := range kinds {
		if k == req.Kind {
			return map[string]any{"op": "list", "kind": k, "items": []any{}, "next": ""}
		}
	}
	for _, k := range artifactKinds {
		if k == req.Kind {
			return map[string]any{"op": "error", "code": artifactErrDisabled}
		}
	}
	return map[string]any{"op": "error", "code": artifactErrUnavailable}
}

// artifactReply sends one reply sealed, within the device's byte allowance. A
// device over its allowance is told only that the host is busy.
func (s *Server) artifactReply(ctx context.Context, tc termConn, dev *artifactDevice, reply map[string]any) bool {
	data, err := json.Marshal(reply)
	if err != nil {
		return false
	}
	if !dev.bytes.Allow(len(data)) {
		data = []byte(`{"op":"error","code":"busy"}`)
	}
	wctx, cancel := context.WithTimeout(ctx, artifactWriteTimeout)
	defer cancel()
	return tc.Write(wctx, websocket.MessageText, data) == nil
}

// pruneBefore drops the times before cut from a list in time order.
func pruneBefore(ts []time.Time, cut time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cut) {
		i++
	}
	return ts[i:]
}
