// Package e2e end-to-end encrypts one terminal socket at a time between a
// paired browser and the desktop it is paired with, so that the relay
// carrying it -- shared or run by a customer alike -- can never read what
// crosses it, the same guarantee push notifications already have. It is a
// byte-for-byte port of flockdeck-relay's own internal/e2e package, which is
// the reference implementation of the scheme: what this package does is
// exactly what a browser (WebCrypto) and this desktop have to do to
// interoperate.
//
// # Why this is a copy, not an import
//
// flockdeck-relay's internal/e2e lives at
// github.com/jmwri/flockdeck-relay/internal/e2e, in a different module from
// this one (github.com/jmwri/flockdeck). Go's internal-import rule only
// allows a package rooted at ".../internal/x" to be imported by code whose
// own import path shares everything before "internal" -- which, across two
// separate modules and repositories, this desktop's code never can. There is
// no way to depend on that package from here, however the module graph is
// arranged, so this file exists instead: the same algorithm, the same
// constants, the same wire format, kept in step by hand. Anyone changing one
// must change the other identically, and the tests in e2e_test.go -- ported
// the same way, from flockdeck-relay's internal/e2e/e2e_test.go -- are what
// catches a copy that has drifted.
//
// # Keys
//
// Every device (a paired browser) and every host (a desktop, this one
// included) holds one long-term P-256 key pair, generated locally with
// GenerateStaticKey. The private half never leaves the machine that made it
// -- not in memory, not on disk, and never over the wire. The public half is
// registered with the relay once (POST /api/v1/device/e2e-key or
// /api/v1/host/e2e-key) and from then on travels exactly where a device's or
// a host's name already travels: in AccountView, to whichever of the
// account's browsers or desktops asks. The relay's part in all of this is
// exactly its part in push notifications -- a switchboard for public keys,
// never a party that could compute a shared secret from them, because it
// never holds a private one.
//
// # Handshake
//
// A browser opening a terminal runs a fresh handshake over that socket
// before anything is typed: StartDeviceHandshake makes an ephemeral key
// pair, good for this one socket alone, and sends its public half as the
// first frame (the "hello"). The desktop reads it, and RespondHostHandshake
// makes its own ephemeral pair and answers with its public half (the
// "response"). Both sides then hold four public keys between them -- the
// device's static and ephemeral, the host's static and ephemeral -- and each
// holds two of the matching private keys, which is enough for each to
// compute the same three Diffie-Hellman terms the other computes from its
// own two:
//
//	T1 = ECDH(device ephemeral, host static)
//	T2 = ECDH(device static,    host ephemeral)
//	T3 = ECDH(device ephemeral, host ephemeral)
//
// T3 alone would be an ordinary anonymous ECDH handshake: secret against
// eavesdropping, but not proof of who is on the other end, since the relay
// forwards the ephemeral keys and could in principle have substituted its
// own. T1 and T2 fold in the two sides' long-term keys, so deriving the
// right session keys from all three terms together needs the device's
// static private key and the host's static private key -- exactly the two
// the relay has never had. That makes the handshake mutually authenticated
// against a relay that merely stores and forwards keys and frames as
// written, though, as with any scheme whose only root of trust is a key
// registered with a server, it does not defend against a relay that
// actively swaps a static public key for its own the moment it is
// registered; closing that gap needs an out-of-band check -- comparing a
// fingerprint on both screens -- which Fingerprint, below, computes from
// the same two static keys the handshake already trusts the relay to hand
// out.
//
// # Fingerprint
//
// Fingerprint(devicePub, hostPub) reduces both static public keys to six
// five-digit groups, such as "04217 91002 55810 30021 88452 71390": a
// SHA-256 of the two keys concatenated, device first, cut into six 5-byte
// chunks, each read as a big-endian integer and taken modulo 100000. A
// browser and a desktop that hold the keys each believes the other's to be
// compute the same thirty digits from them alone -- no session, no
// handshake, nothing that has to have happened yet -- so the two can be
// compared once, at pairing, or at any later moment either side is unsure,
// without opening a terminal first. A relay that swapped a static key at
// registration is holding a different key than the genuine device or host
// now has, on at least one side of the pair, and that shows up as two
// different fingerprints on the two screens: the one part of this scheme a
// person, not a computation, has to check, because a relay that lies about
// a key can just as well have this package compute an agreeing answer from
// the lie. Thirty decimal digits is a search space -- 10^30 -- no attacker
// picking a substitute key gets to brute-force into matching the genuine
// one's fingerprint, which is what makes comparing thirty digits as good as
// comparing the keys themselves byte for byte.
//
// T1, T2 and T3 are concatenated and fed to HKDF-SHA256 (RFC 5869), salted
// with both ephemeral public keys so the extract is bound to this exact
// exchange, to make two independent AES-256-GCM keys -- one for each
// direction, so the two ends never encrypt with the same key -- and two
// 4-byte nonce prefixes to go with them. Losing an ephemeral private key
// later (a browser tab closed, a desktop restarted) reveals nothing about a
// past session: T3, and with it the whole derived secret, cannot be
// recomputed without it. That is this scheme's forward secrecy.
//
// # Frames
//
// Once a Session is derived, every message that follows on the same socket
// -- one terminal keystroke, one chunk of output -- is one call to Seal on
// the way out and Open on the way in. A frame is a version byte, an 8-byte
// big-endian counter and an AES-256-GCM-sealed body; the version and which
// of the two sides sent it are folded into the AEAD's associated data, and
// the counter must strictly increase by exactly one each time, which an
// ordered, reliable WebSocket -- what every terminal socket already is --
// never gives a legitimate frame reason to violate. A relay that dropped,
// reordered, duplicated or altered a single byte of a frame would only make
// Open refuse it; there is nothing in the scheme it could change instead.
//
// # What the relay does with any of this
//
// Nothing. The relay never imports this package (nor could it import this
// copy of it, being in the other module). It stores and hands out public
// keys exactly as it already does with a device's push subscription keys,
// and it carries a terminal socket's bytes exactly as it already carries
// every other stream through the tunnel -- opaque past the outer framing
// smux and WebSocket need to deliver them at all. A relay that held every
// byte this package ever sent or received could not read a single terminal
// keystroke without one of the two static private keys, which it was never
// given.
package e2e

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Version is the wire version of every handshake message and frame this
// package makes. It travels in a data frame's first byte and in the AEAD's
// associated data, so a frame made under a later or earlier version of this
// scheme is refused rather than misread as this one's.
const Version byte = 1

// Role says which end of a session a Session is, which decides which of its
// two directional keys it seals with and which it opens with, and which
// side's long-term and ephemeral keys take which position in the handshake
// both ends must compute identically.
type Role byte

// The two roles. Their numeric value is part of the wire format -- it goes
// into the AEAD's associated data as the sender's role -- and so is fixed.
const (
	RoleDevice Role = 0
	RoleHost   Role = 1
)

func (r Role) String() string {
	if r == RoleHost {
		return "host"
	}
	return "device"
}

// curve is the elliptic curve every key in this package is on: P-256, the
// same one flockdeck-relay's own push subscription and VAPID keys use, so a
// Go implementation of either shares the same stdlib type and the same
// encoding.
func curve() ecdh.Curve { return ecdh.P256() }

const (
	// keyLen is an AES-256 key.
	keyLen = 32
	// noncePrefixLen and counterLen split a GCM nonce (12 bytes) into a
	// fixed part, derived once per session per direction, and a part that
	// changes on every frame, so that two frames of the same session and
	// direction never reuse a nonce as long as the counter does not repeat --
	// which Seal enforces by construction and Open enforces by refusing
	// anything but the next one expected.
	noncePrefixLen = 4
	counterLen     = 8
	nonceLen       = noncePrefixLen + counterLen
)

// The HKDF-Expand info strings that turn one session secret into four
// independent values. Distinct info makes each one unrecoverable from any
// other even though all four come from the same extract.
const (
	infoDeviceToHostKey   = "flockdeck terminal e2e v1 device->host key"
	infoHostToDeviceKey   = "flockdeck terminal e2e v1 host->device key"
	infoDeviceToHostNonce = "flockdeck terminal e2e v1 device->host nonce"
	infoHostToDeviceNonce = "flockdeck terminal e2e v1 host->device nonce"
)

// GenerateStaticKey makes a fresh long-term P-256 key pair: what a device or
// a host generates once, keeps the private half of forever (until it
// chooses to rotate), and registers the public half of with the relay.
func GenerateStaticKey() (*ecdh.PrivateKey, error) {
	priv, err := curve().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("e2e: generate a static key: %w", err)
	}
	return priv, nil
}

// EncodePublicKey is how a public key is given to the relay to store, and
// how the relay gives one back: unpadded base64url of the uncompressed P-256
// point (65 bytes), the same encoding flockdeck-relay's push.go already uses
// for a subscription's p256dh.
func EncodePublicKey(pub *ecdh.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(pub.Bytes())
}

// DecodePublicKey reads a public key as EncodePublicKey writes it, refusing
// anything that is not base64url or not a point on the curve -- which is all
// the relay itself ever needs to check of a key it is asked to store, having
// no way to know anything more about whether it is genuinely the sender's.
func DecodePublicKey(s string) (*ecdh.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("e2e: public key is not base64url: %w", err)
	}
	pub, err := curve().NewPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("e2e: public key is not a P-256 point: %w", err)
	}
	return pub, nil
}

// fingerprintGroups is how many 5-digit groups Fingerprint makes, and
// fingerprintGroupBytes how many bytes of the SHA-256 digest go into each
// one -- six groups of five bytes uses the digest's first 30 bytes, leaving
// its last 2 unused.
const (
	fingerprintGroups     = 6
	fingerprintGroupBytes = 5
	fingerprintGroupMod   = 100000
)

// Fingerprint reduces a device's and a host's long-term public keys to a
// short code a person can compare on two screens: the out-of-band check the
// handshake's own mutual authentication cannot make for itself against a
// relay that swapped a static key the moment it was registered (see this
// package's own doc for what it closes and why thirty digits is enough).
//
// Both ends of a pairing compute the same code from the same two keys
// regardless of which one calls it -- devicePub and hostPub always mean the
// device's and the host's static keys, never "mine" and "theirs" -- so it is
// devicePub first, hostPub second, on both sides, every time.
func Fingerprint(devicePub, hostPub *ecdh.PublicKey) string {
	h := sha256.New()
	h.Write(devicePub.Bytes())
	h.Write(hostPub.Bytes())
	sum := h.Sum(nil)

	groups := make([]string, fingerprintGroups)
	for i := range groups {
		chunk := sum[i*fingerprintGroupBytes : (i+1)*fingerprintGroupBytes]
		var v uint64
		for _, b := range chunk {
			v = v<<8 | uint64(b)
		}
		groups[i] = fmt.Sprintf("%05d", v%fingerprintGroupMod)
	}
	return strings.Join(groups, " ")
}

// DeviceHandshake is a browser's side of opening one terminal session, from
// the moment it makes its ephemeral key until the desktop's response
// completes it.
type DeviceHandshake struct {
	staticPriv    *ecdh.PrivateKey
	ephPriv       *ecdh.PrivateKey
	hostStaticPub *ecdh.PublicKey
}

// StartDeviceHandshake begins a browser's side of a fresh terminal session
// with the desktop whose long-term public key is hostStaticPub -- read from
// the relay's own account view, where every desktop's key already travels.
// It returns the hello message to send as the terminal socket's very first
// frame, ahead of anything a person types or the desktop sends back.
func StartDeviceHandshake(deviceStaticPriv *ecdh.PrivateKey, hostStaticPub *ecdh.PublicKey) (*DeviceHandshake, []byte, error) {
	ephPriv, err := curve().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: device handshake: %w", err)
	}
	h := &DeviceHandshake{staticPriv: deviceStaticPriv, ephPriv: ephPriv, hostStaticPub: hostStaticPub}
	return h, ephPriv.PublicKey().Bytes(), nil
}

// Finish completes a browser's handshake with the desktop's response -- its
// ephemeral public key, exactly as StartDeviceHandshake's hello was -- and
// returns the session the two now independently share.
func (h *DeviceHandshake) Finish(response []byte) (*Session, error) {
	hostEphPub, err := curve().NewPublicKey(response)
	if err != nil {
		return nil, fmt.Errorf("e2e: the desktop's handshake response: %w", err)
	}
	return newSession(RoleDevice, h.staticPriv, h.ephPriv, h.hostStaticPub, hostEphPub)
}

// RespondHostHandshake is a desktop's whole side of the handshake in one
// call: given the browser's hello and the long-term public key the relay
// says that device holds (fetched from /api/v1/host/devices and matched to
// this socket by the Flockdeck-Remote-Device header the relay already sets,
// which names the device without the device having to say so itself), it
// returns the session and the response to send back as the terminal
// socket's second frame.
func RespondHostHandshake(hostStaticPriv *ecdh.PrivateKey, deviceStaticPub *ecdh.PublicKey, hello []byte) (*Session, []byte, error) {
	deviceEphPub, err := curve().NewPublicKey(hello)
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: the browser's handshake hello: %w", err)
	}
	hostEphPriv, err := curve().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("e2e: host handshake: %w", err)
	}
	s, err := newSession(RoleHost, hostStaticPriv, hostEphPriv, deviceStaticPub, deviceEphPub)
	if err != nil {
		return nil, nil, err
	}
	return s, hostEphPriv.PublicKey().Bytes(), nil
}

// newSession derives the session both sides reach independently, given
// role, this side's own static and ephemeral private keys, and the peer's
// static and ephemeral public keys. See the package doc for the three
// Diffie-Hellman terms and how they are combined.
func newSession(role Role, ownStaticPriv, ownEphPriv *ecdh.PrivateKey, peerStaticPub, peerEphPub *ecdh.PublicKey) (*Session, error) {
	var deviceStaticPub, hostStaticPub *ecdh.PublicKey
	var deviceEphPub, hostEphPub *ecdh.PublicKey
	var t1, t2, t3 []byte
	var err error

	switch role {
	case RoleDevice:
		deviceStaticPub, deviceEphPub = ownStaticPriv.PublicKey(), ownEphPriv.PublicKey()
		hostStaticPub, hostEphPub = peerStaticPub, peerEphPub
		if t1, err = ownEphPriv.ECDH(hostStaticPub); err != nil {
			return nil, fmt.Errorf("e2e: device ephemeral x host static: %w", err)
		}
		if t2, err = ownStaticPriv.ECDH(hostEphPub); err != nil {
			return nil, fmt.Errorf("e2e: device static x host ephemeral: %w", err)
		}
		if t3, err = ownEphPriv.ECDH(hostEphPub); err != nil {
			return nil, fmt.Errorf("e2e: device ephemeral x host ephemeral: %w", err)
		}
	case RoleHost:
		hostStaticPub, hostEphPub = ownStaticPriv.PublicKey(), ownEphPriv.PublicKey()
		deviceStaticPub, deviceEphPub = peerStaticPub, peerEphPub
		if t1, err = ownStaticPriv.ECDH(deviceEphPub); err != nil {
			return nil, fmt.Errorf("e2e: host static x device ephemeral: %w", err)
		}
		if t2, err = ownEphPriv.ECDH(deviceStaticPub); err != nil {
			return nil, fmt.Errorf("e2e: host ephemeral x device static: %w", err)
		}
		if t3, err = ownEphPriv.ECDH(deviceEphPub); err != nil {
			return nil, fmt.Errorf("e2e: host ephemeral x device ephemeral: %w", err)
		}
	default:
		return nil, fmt.Errorf("e2e: unknown role %d", role)
	}

	ikm := make([]byte, 0, len(t1)+len(t2)+len(t3))
	ikm = append(ikm, t1...)
	ikm = append(ikm, t2...)
	ikm = append(ikm, t3...)

	// The transcript salt -- both ephemeral public keys, device's first --
	// binds the derived keys to this exact exchange.
	salt := make([]byte, 0, len(deviceEphPub.Bytes())+len(hostEphPub.Bytes()))
	salt = append(salt, deviceEphPub.Bytes()...)
	salt = append(salt, hostEphPub.Bytes()...)

	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, fmt.Errorf("e2e: derive the session secret: %w", err)
	}

	deviceToHostKey, err := hkdf.Expand(sha256.New, prk, infoDeviceToHostKey, keyLen)
	if err != nil {
		return nil, fmt.Errorf("e2e: derive the device->host key: %w", err)
	}
	hostToDeviceKey, err := hkdf.Expand(sha256.New, prk, infoHostToDeviceKey, keyLen)
	if err != nil {
		return nil, fmt.Errorf("e2e: derive the host->device key: %w", err)
	}
	deviceToHostPrefix, err := hkdf.Expand(sha256.New, prk, infoDeviceToHostNonce, noncePrefixLen)
	if err != nil {
		return nil, fmt.Errorf("e2e: derive the device->host nonce prefix: %w", err)
	}
	hostToDevicePrefix, err := hkdf.Expand(sha256.New, prk, infoHostToDeviceNonce, noncePrefixLen)
	if err != nil {
		return nil, fmt.Errorf("e2e: derive the host->device nonce prefix: %w", err)
	}

	sendKey, sendPrefix, sendRole := deviceToHostKey, deviceToHostPrefix, RoleDevice
	recvKey, recvPrefix, recvRole := hostToDeviceKey, hostToDevicePrefix, RoleHost
	if role == RoleHost {
		sendKey, sendPrefix, sendRole = hostToDeviceKey, hostToDevicePrefix, RoleHost
		recvKey, recvPrefix, recvRole = deviceToHostKey, deviceToHostPrefix, RoleDevice
	}

	sendAEAD, err := newAEAD(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := newAEAD(recvKey)
	if err != nil {
		return nil, err
	}
	return &Session{
		sendAEAD: sendAEAD, sendPrefix: sendPrefix, sendRole: sendRole,
		recvAEAD: recvAEAD, recvPrefix: recvPrefix, recvRole: recvRole,
	}, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("e2e: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("e2e: %w", err)
	}
	return gcm, nil
}

// Session is one terminal socket's derived keys: two AES-256-GCM ciphers,
// one for each direction, each with its own nonce prefix and its own frame
// counter. It is good for the life of the socket it was made for alone --
// nothing in this package ever persists one -- and is not safe for
// concurrent use: a socket's reader calls Open and its writer calls Seal,
// each from one goroutine of its own, exactly as a typical connection's
// read and write sides already run.
type Session struct {
	sendAEAD    cipher.AEAD
	sendPrefix  []byte // noncePrefixLen bytes, fixed for the session
	sendRole    Role   // whose frames sendAEAD makes -- this side's own
	sendCounter uint64

	recvAEAD    cipher.AEAD
	recvPrefix  []byte
	recvRole    Role // whose frames recvAEAD opens -- the peer's
	recvCounter uint64
}

// aad is what a frame's AEAD authenticates beyond its own ciphertext: the
// wire version and which side sent it, so a frame can never be mistaken for
// one of a different scheme version or replayed as though it came from the
// other direction.
func aad(sender Role) []byte { return []byte{Version, byte(sender)} }

func (s *Session) nonce(prefix []byte, counter uint64) []byte {
	nonce := make([]byte, 0, nonceLen)
	nonce = append(nonce, prefix...)
	return binary.BigEndian.AppendUint64(nonce, counter)
}

// Seal encrypts plaintext as the next frame of this session's outgoing
// direction: a wire version byte, an 8-byte big-endian counter -- 0 for the
// first frame after the handshake, then strictly one more each call -- and
// the AEAD's output, ciphertext followed by its 16-byte tag. The counter is
// never reused for the life of a Session; at one frame a nanosecond it would
// take longer than the age of the universe to repeat, which is the only way
// two frames could ever share a nonce.
func (s *Session) Seal(plaintext []byte) []byte {
	nonce := s.nonce(s.sendPrefix, s.sendCounter)
	frame := make([]byte, 0, 1+counterLen+len(plaintext)+aeadOverhead)
	frame = append(frame, Version)
	frame = binary.BigEndian.AppendUint64(frame, s.sendCounter)
	frame = s.sendAEAD.Seal(frame, nonce, plaintext, aad(s.sendRole))
	s.sendCounter++
	return frame
}

// aeadOverhead is AES-GCM's authentication tag, appended to Seal's output
// after the ciphertext. It only sizes Seal's initial allocation; Open reads
// whatever length the AEAD itself expects.
const aeadOverhead = 16

// errShortFrame is Open's own failure, distinct from "does not authenticate"
// (wrapping the AEAD's own error), so a caller can tell a frame that was
// never valid at all from one this session specifically refuses.
var errShortFrame = errors.New("e2e: frame is shorter than a header")

// Open authenticates and decrypts a frame Seal made of this session's
// incoming direction. It refuses one of the wrong wire version, one whose
// counter is not exactly the next expected -- an old frame replayed, or one
// out of order, which the ordered, reliable socket a terminal already runs
// on gives a genuine frame no way to arrive as -- or one that does not
// authenticate at all, which is any tampering with it, the relay's
// included: nothing this package ever gives the relay lets it produce a
// frame that passes this check.
func (s *Session) Open(frame []byte) ([]byte, error) {
	if len(frame) < 1+counterLen {
		return nil, errShortFrame
	}
	if frame[0] != Version {
		return nil, fmt.Errorf("e2e: frame is wire version %d, this session speaks %d", frame[0], Version)
	}
	counter := binary.BigEndian.Uint64(frame[1 : 1+counterLen])
	if counter != s.recvCounter {
		return nil, fmt.Errorf("e2e: frame counter %d, want %d", counter, s.recvCounter)
	}
	nonce := s.nonce(s.recvPrefix, counter)
	plaintext, err := s.recvAEAD.Open(nil, nonce, frame[1+counterLen:], aad(s.recvRole))
	if err != nil {
		return nil, fmt.Errorf("e2e: frame does not authenticate: %w", err)
	}
	s.recvCounter++
	return plaintext, nil
}
