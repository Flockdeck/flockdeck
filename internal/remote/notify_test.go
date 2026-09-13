package remote

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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// browser is a browser's side of a push subscription: the key pair and
// secret it made, the private half of which never leaves it.
type browser struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newBrowser(t *testing.T) browser {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return browser{priv: priv, auth: auth}
}

func (b browser) device(id string) PushDevice {
	enc := base64.RawURLEncoding
	return PushDevice{DeviceID: id, P256dh: enc.EncodeToString(b.priv.PublicKey().Bytes()), Auth: enc.EncodeToString(b.auth)}
}

// open reads a sealed message the way the browser does (RFC 8291 and RFC
// 8188), or fails.
func (b browser) open(payload string) ([]byte, bool) {
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(body) < 21 {
		return nil, false
	}
	salt, idlen := body[:16], int(body[20])
	if len(body) < 21+idlen {
		return nil, false
	}
	keyid, ct := body[21:21+idlen], body[21+idlen:]
	as, err := ecdh.P256().NewPublicKey(keyid)
	if err != nil {
		return nil, false
	}
	shared, err := b.priv.ECDH(as)
	if err != nil {
		return nil, false
	}
	info := append([]byte("WebPush: info\x00"), b.priv.PublicKey().Bytes()...)
	info = append(info, keyid...)
	ikm, _ := hkdf.Key(sha256.New, shared, b.auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, false
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		return nil, false
	}
	return plain[:len(plain)-1], true
}

// A sealed notification opens with the browser's own key and no other, and
// every one is the same length, so its size says nothing about what it says.
func TestASealedNotificationOpensOnlyForItsBrowser(t *testing.T) {
	b, other := newBrowser(t), newBrowser(t)
	d := b.device("d1")
	short, err := Seal(d.P256dh, d.Auth, []byte(`{"title":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	long, err := Seal(d.P256dh, d.Auth, bytes.Repeat([]byte("x"), 900))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := b.open(short); !ok || string(got) != `{"title":"a"}` {
		t.Errorf("the browser opened %q, %v", got, ok)
	}
	if _, ok := other.open(short); ok {
		t.Error("another browser opened a message sealed for this one")
	}
	if len(short) != len(long) {
		t.Errorf("a short message is %d characters and a long one %d: the length gives the message away", len(short), len(long))
	}
	if _, err := Seal(d.P256dh, d.Auth, bytes.Repeat([]byte("x"), pushRecord)); err == nil {
		t.Error("a message too long to be carried was sealed")
	}
	for name, dev := range map[string]PushDevice{
		"no key":         {P256dh: "", Auth: d.Auth},
		"a short secret": {P256dh: d.P256dh, Auth: "c2hvcnQ"},
	} {
		if _, err := Seal(dev.P256dh, dev.Auth, []byte("{}")); err == nil {
			t.Errorf("%s: sealed", name)
		}
	}
}

// Push asks the relay for the devices, seals the notification for each, and
// posts only the sealed copies as this machine. A device whose keys changed in
// between is asked about again and sent a copy it can open; the others are
// not sent a second.
func TestPushSealsForEachDevice(t *testing.T) {
	phone, laptop, replaced := newBrowser(t), newBrowser(t), newBrowser(t)
	var mu sync.Mutex
	asked := 0
	var posts [][]PushMessage
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auths = append(auths, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/host/push/devices":
			asked++
			devices := []PushDevice{phone.device("phone"), laptop.device("laptop")}
			if asked > 1 {
				// The laptop subscribed afresh after it was first asked about.
				devices[1] = replaced.device("laptop")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": devices})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/host/notify":
			var req struct {
				Messages []PushMessage `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			posts = append(posts, req.Messages)
			if len(posts) == 1 {
				_, _ = w.Write([]byte(`{"sent":1,"stale":1}`))
				return
			}
			_, _ = w.Write([]byte(`{"sent":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{Relay: srv.URL, Token: "fdh_test"}
	n := Notification{Title: "claude · api needs you", Body: "On desk", URL: "/d/h1/p1", Tag: "flockdeck-h1"}
	sent, err := c.Push(context.Background(), n)
	if err != nil || sent != 2 {
		t.Fatalf("Push = %d, %v", sent, err)
	}
	for _, a := range auths {
		if a != "Bearer fdh_test" {
			t.Errorf("the relay was called with %q", a)
		}
	}
	want, _ := json.Marshal(n)
	if len(posts) != 2 || len(posts[0]) != 2 || len(posts[1]) != 1 || posts[1][0].DeviceID != "laptop" {
		t.Fatalf("the relay was posted %+v", posts)
	}
	for i, m := range posts[0] {
		b := []browser{phone, laptop}[i]
		if got, ok := b.open(m.Payload); !ok || !bytes.Equal(got, want) {
			t.Errorf("%s opened %q, %v", m.DeviceID, got, ok)
		}
		if bytes.Contains([]byte(m.Payload), []byte("needs you")) {
			t.Error("the relay was handed the notification in the clear")
		}
	}
	if got, ok := replaced.open(posts[1][0].Payload); !ok || !bytes.Equal(got, want) || posts[1][0].P256dh != replaced.device("").P256dh {
		t.Errorf("the laptop's new keys were not sealed for: %q, %v", got, ok)
	}
}

// A relay's refusal comes back in its words, and is not taken for this
// machine's credentials being spent.
func TestPushRefusedIsTheRelaysWord(t *testing.T) {
	b := newBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": []PushDevice{b.device("d1")}})
			return
		}
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"Your remote access trial has ended."}`))
	}))
	defer srv.Close()
	c := &Client{Relay: srv.URL, Token: "fdh_test"}
	_, err := c.Push(context.Background(), Notification{Title: "x"})
	if err == nil || err.Error() != "the relay said: Your remote access trial has ended." {
		t.Errorf("a refusal: %v", err)
	}
	if IsRevoked(err) {
		t.Error("a plan's refusal was taken for this machine's credentials being spent")
	}
}
