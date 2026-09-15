package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/e2e"
)

// End-to-end encrypting a terminal reached through the relay (see
// internal/e2e for the scheme, and internal/remote's E2ECapable and
// E2ERespond for this host's own identity and its lookup of a device's
// registered key). This file is where a terminal socket actually uses it:
// running the handshake as handlePTY's first exchange, and wrapping the
// socket so every frame after is one internal/e2e frame instead of one
// terminal message.
//
// Nothing here changes what a local window sees: none of this runs unless
// fromRemote(r) is true, and even then only once E2ECapable has already
// said this host and the connecting device both have a key on file.

// termConn is what handlePTY and its helpers read and write a terminal
// socket through: *websocket.Conn itself, unless the handshake in handlePTY
// end-to-end encrypts the socket, in which case it is an *e2eConn wrapping
// the same connection. The two behave identically from the caller's side --
// one Read or Write is one WebSocket message either way -- so nothing past
// the handshake needs to know which one it has. Ping and the close
// handshake stay on the real *websocket.Conn regardless (see handlePTY):
// they are WebSocket control frames, not terminal traffic, and carry
// nothing this scheme needs to hide.
type termConn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, data []byte) error
}

// e2eHandshakeTimeout bounds how long a terminal socket waits for the
// browser's handshake hello, once E2ECapable has already found a key on
// file for both this host and the device asking. A compliant browser sends
// it as the socket's very first frame, before anything is typed (see
// internal/e2e's package doc), so this is generous against a slow link, not
// a slow person. It is a variable so a test does not have to sit through it.
var e2eHandshakeTimeout = 10 * time.Second

// e2eHandshake runs this host's whole side of a fresh terminal handshake
// over conn: reading the browser's hello as the socket's first frame,
// asking ra to answer it -- which knows this host's own identity and looks
// up the device's registered public key -- and writing the response back as
// the socket's second frame. Both frames are exactly internal/e2e's wire
// format, raw public key bytes, and cross the socket unencrypted: there is
// nothing yet to encrypt them with.
//
// It is only ever called once E2ECapable has said both sides have a key on
// file, so a failure here -- the hello never arrives, or does not parse, or
// the relay's own roster changed underneath the two calls -- is a fault,
// not the ordinary "this browser predates encryption" case; the caller
// closes the socket rather than falling back to plaintext, since a relay
// stripping a genuine hello to force that fallback is exactly the downgrade
// this is guarding against.
func (s *Server) e2eHandshake(ctx context.Context, ra RemoteAccess, conn *websocket.Conn, device string) (*e2e.Session, error) {
	hctx, cancel := context.WithTimeout(ctx, e2eHandshakeTimeout)
	defer cancel()
	typ, hello, err := conn.Read(hctx)
	if err != nil {
		return nil, fmt.Errorf("read the handshake hello: %w", err)
	}
	if typ != websocket.MessageBinary {
		return nil, errors.New("the handshake hello arrived as a text frame, not binary")
	}
	sess, response, err := ra.E2ERespond(ctx, device, hello)
	if err != nil {
		return nil, fmt.Errorf("respond to the handshake: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, response); err != nil {
		return nil, fmt.Errorf("send the handshake response: %w", err)
	}
	return sess, nil
}

// The one-byte tag e2eConn puts ahead of a frame's plaintext before sealing
// it, and reads back off after opening one, so that Open's result can be
// told apart as a keystroke (or a chunk of output) from a resize or a
// stream header -- the distinction handlePTY otherwise reads off the
// WebSocket message's own type, which is lost once every sealed frame has
// to cross the wire as opaque binary (AES-GCM ciphertext is not valid
// UTF-8, so it could never be sent as a WebSocket text frame regardless of
// what it started as).
const (
	e2eTagBinary byte = 0
	e2eTagText   byte = 1
)

// e2eConn is a terminal socket once its handshake has derived a session: a
// termConn that seals every message on the way out and opens every message
// on the way in, so the rest of handlePTY reads and writes exactly as it
// did before, and the relay carrying conn sees only internal/e2e frames.
//
// It is not safe for concurrent Reads, nor for concurrent Writes, which
// matches internal/e2e.Session's own contract and how handlePTY already
// uses a terminal socket: one goroutine (readInput) ever reads it, and one
// (handlePTY's own loop) ever writes it.
type e2eConn struct {
	conn *websocket.Conn
	sess *e2e.Session
}

// Read opens the next sealed frame and returns the message it was made
// from: its kind, from the tag e2eTag put ahead of the plaintext, and the
// plaintext itself. A frame that does not authenticate, or that is not
// binary -- there is nothing else a peer running this scheme ever sends
// once the handshake is done -- ends the connection, the same as any other
// read error does.
func (c *e2eConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	typ, data, err := c.conn.Read(ctx)
	if err != nil {
		return 0, nil, err
	}
	if typ != websocket.MessageBinary {
		return 0, nil, fmt.Errorf("e2e: a %v frame on an encrypted terminal socket", typ)
	}
	plain, err := c.sess.Open(data)
	if err != nil {
		return 0, nil, err
	}
	if len(plain) == 0 {
		return 0, nil, errors.New("e2e: an empty frame")
	}
	tag, payload := plain[0], plain[1:]
	if tag == e2eTagText {
		return websocket.MessageText, payload, nil
	}
	return websocket.MessageBinary, payload, nil
}

// Write seals data, tagged with the WebSocket message type it was written
// as, and sends it as one binary frame -- binary because the seal's output
// is ciphertext, never valid UTF-8, whatever kind of message it started as.
func (c *e2eConn) Write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	tag := e2eTagBinary
	if typ == websocket.MessageText {
		tag = e2eTagText
	}
	plain := make([]byte, 0, 1+len(data))
	plain = append(plain, tag)
	plain = append(plain, data...)
	return c.conn.Write(ctx, websocket.MessageBinary, c.sess.Seal(plain))
}
