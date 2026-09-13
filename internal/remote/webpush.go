package remote

// Push notifications are encrypted here, on the desktop, for each device that
// asked for them, and the relay only signs and posts what it is handed. The
// key that opens a message never leaves the browser it was made in, so
// neither the relay nor the push service that carries the message can read it
// (RFC 8291, with the aes128gcm coding of RFC 8188).

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// pushRecord is the length every sealed message's content is padded to: the
// notification's JSON, its delimiter and zeros. A push service then sees
// messages that are all one size, and learns nothing from how long a pane's
// name is.
const pushRecord = 1024

// maxPushDevices is as many devices as the relay takes messages for at once.
const maxPushDevices = 32

// Notification is what a push says, as the client's service worker reads it:
// its title and body, the page a tap on it opens, and its tag, which a newer
// notification with the same one replaces on the device.
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// PushDevice is a device of the account that asked to be sent pushes, as the
// relay describes it: the keys a message to it is encrypted to, and nothing
// about where it is reached.
type PushDevice struct {
	DeviceID string `json:"deviceId"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// PushMessage is one device's notification, sealed for it alone, and the key
// it was sealed to.
type PushMessage struct {
	DeviceID string `json:"deviceId"`
	P256dh   string `json:"p256dh"`
	Payload  string `json:"payload"`
}

// PushDevices asks the relay which of the account's devices asked to be sent
// pushes, and their keys.
func (c *Client) PushDevices(ctx context.Context) ([]PushDevice, error) {
	var out struct {
		Devices []PushDevice `json:"devices"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/host/push/devices", nil, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// Push sends a notification to every device of the account that asked for
// them, each copy sealed here for its own device, and says how many a push
// service took. A relay that sends none, or an account whose plan does not
// include them, says so in its own words: whether pushes are sent is the
// relay's to decide.
//
// A device that replaced its keys between the asking and the sending is not
// sent a message it could not open; the relay says so, and is asked once more
// for the keys of the devices that changed.
func (c *Client) Push(ctx context.Context, n Notification) (int, error) {
	plain, err := fitNotification(n)
	if err != nil {
		return 0, err
	}
	sealed := map[string]string{} // by device: the key its message was sealed to
	sent := 0
	for attempt := 0; attempt < 2; attempt++ {
		devices, err := c.PushDevices(ctx)
		if err != nil {
			return sent, err
		}
		var msgs []PushMessage
		for _, d := range devices {
			if key, ok := sealed[d.DeviceID]; ok && key == d.P256dh {
				continue
			}
			if len(msgs) == maxPushDevices {
				break
			}
			payload, err := Seal(d.P256dh, d.Auth, plain)
			if err != nil {
				return sent, fmt.Errorf("seal a notification for a paired device: %w", err)
			}
			sealed[d.DeviceID] = d.P256dh
			msgs = append(msgs, PushMessage{DeviceID: d.DeviceID, P256dh: d.P256dh, Payload: payload})
		}
		if len(msgs) == 0 {
			break
		}
		var out struct {
			Sent  int `json:"sent"`
			Stale int `json:"stale"`
		}
		if err := c.call(ctx, http.MethodPost, "/api/v1/host/notify", map[string][]PushMessage{"messages": msgs}, &out); err != nil {
			return sent, err
		}
		sent += out.Sent
		if out.Stale == 0 {
			break
		}
	}
	return sent, nil
}

// fitNotification is a notification's JSON, shortened if it is longer than one
// push carries: its body first, then its title, each cut to half with an
// ellipsis until it fits. Names are what make one long, and a notification
// that says less is better than none.
func fitNotification(n Notification) ([]byte, error) {
	for {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		// "<" is six bytes escaped, and nothing here is put in a page.
		enc.SetEscapeHTML(false)
		if err := enc.Encode(n); err != nil {
			return nil, err
		}
		plain := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		if len(plain) < pushRecord {
			return plain, nil
		}
		switch {
		case n.Body != "":
			n.Body = halve(n.Body)
		case n.Title != "":
			n.Title = halve(n.Title)
		default:
			return nil, fmt.Errorf("a notification of %d bytes is longer than the %d a push carries", len(plain), pushRecord-1)
		}
	}
}

// halve is the first half of s's runes and an ellipsis, or nothing once there
// is a rune or less left.
func halve(s string) string {
	r := []rune(strings.TrimSuffix(s, "…"))
	if len(r) <= 1 {
		return ""
	}
	return string(r[:len(r)/2]) + "…"
}

// Seal encrypts a message to a browser's push subscription -- its P-256
// public key and its 16-byte auth secret, base64url as the browser gives them
// -- and gives back the aes128gcm body, base64url, which only that browser can
// open. Every body is the same length, whatever the message.
func Seal(p256dh, auth string, plain []byte) (string, error) {
	if len(plain) > pushRecord-1 {
		return "", fmt.Errorf("a notification of %d bytes is longer than the %d a push carries", len(plain), pushRecord-1)
	}
	ua, err := decodeB64URL(p256dh)
	if err != nil {
		return "", errors.New("the device's push key is not base64url")
	}
	uaPub, err := ecdh.P256().NewPublicKey(ua)
	if err != nil {
		return "", errors.New("the device's push key is not a P-256 public key")
	}
	secret, err := decodeB64URL(auth)
	if err != nil || len(secret) != 16 {
		return "", errors.New("the device's push secret is not 16 bytes of base64url")
	}

	// A key pair of our own for this one message, whose public half goes in
	// the header; the shared secret it makes with the browser's key, mixed
	// with the browser's auth secret, is the input keying material (RFC 8291,
	// section 3.4).
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	shared, err := as.ECDH(uaPub)
	if err != nil {
		return "", err
	}
	info := append([]byte("WebPush: info\x00"), ua...)
	info = append(info, as.PublicKey().Bytes()...)
	ikm, err := hkdf.Key(sha256.New, shared, secret, string(info), 32)
	if err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return "", err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// One record: the message, the delimiter that says it is the last (2),
	// and zeros to the fixed length (RFC 8188, section 2).
	record := make([]byte, pushRecord)
	copy(record, plain)
	record[len(plain)] = 2

	// The header: the salt, the record size, and our public key as the key id.
	body := make([]byte, 0, 16+4+1+65+pushRecord+gcm.Overhead())
	body = append(body, salt...)
	body = binary.BigEndian.AppendUint32(body, uint32(pushRecord+gcm.Overhead()))
	body = append(body, 65)
	body = append(body, as.PublicKey().Bytes()...)
	body = gcm.Seal(body, nonce, record, nil)
	return base64.RawURLEncoding.EncodeToString(body), nil
}

// decodeB64URL reads base64url, padded or not, which is how browsers write
// their keys.
func decodeB64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
}
