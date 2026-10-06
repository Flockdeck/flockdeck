package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
)

// evil is JSON text, with the escapes left as \u sequences so it can sit inside
// a JSON string, that holds what must never reach a terminal: ESC with a CSI
// sequence, an OSC title with BEL, a bidirectional override and isolate, a
// zero-width space, a newline and a carriage return.
var evil = `ok\u001b[2J\u001b]0;pwned\u0007` + `\` + `u202eevil` + `\` + `u2066` + `\` + `u200b` + `\n\rend`

// evilPlain is what evil comes out as once Plain has dropped the control and
// non-printing runes. The printable remains of an escape sequence stay as text,
// which is harmless without its ESC.
const evilPlain = `ok[2J]0;pwnedevilend`

func cleanOf(t *testing.T, where, s string) {
	t.Helper()
	for _, r := range s {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			t.Errorf("%s holds %U: %q", where, r, s)
			return
		}
	}
}

func TestPlain(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain text, with punctuation: é 日本 😀", "plain text, with punctuation: é 日本 😀"},
		{"a\x1b[31mb", "a[31mb"},
		{"a\x1b]0;title\x07b", "a]0;titleb"},
		{"a" + string(rune(0x202e)) + "b" + string(rune(0x202a)) + "c" + string(rune(0x2066)) + "d" + string(rune(0x2069)) + "e", "abcde"},
		{"a" + string(rune(0x200b)) + "b" + string(rune(0xfeff)) + "c", "abc"},
		{"a\nb\rc\td\x00e\x7ff", "abcdef"},
		{"\u0085\u009b31m", "31m"},
		{"", ""},
	} {
		if got := Plain(tc.in); got != tc.want {
			t.Errorf("Plain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// What the relay says in a refusal is cleaned however it reaches the person.
func TestRefusalsAreCleaned(t *testing.T) {
	err := decodeError(http.StatusConflict, []byte(`{"error":"`+evil+`"}`))
	api := err.(*APIError)
	if api.Message != evilPlain {
		t.Errorf("Message = %q, want %q", api.Message, evilPlain)
	}
	cleanOf(t, "APIError.Error", err.Error())
	// One built another way is cleaned when it is worded.
	cleanOf(t, "APIError.Error of a raw message", (&APIError{Status: 400, Message: "x\x1b[31m" + string(rune(0x202e)) + "y"}).Error())
	if got := (&APIError{Status: 400, Message: "\x1b" + string(rune(0x202e))}).Error(); got != "the relay answered 400 Bad Request" {
		t.Errorf("an error that is nothing once cleaned = %q, want the status", got)
	}
	cleanOf(t, "RevokedReason", RevokedReason(&APIError{Status: 401, Message: "gone\x1b]0;x\x07" + string(rune(0x202e))}))
}

func TestStartRefusalsAndLinksAreCleaned(t *testing.T) {
	quickPoll(t)
	f := newFakeRelay(t)
	f.requireVerify = true
	f.startStatus, f.startError = http.StatusConflict, evil
	_, err := VerifyEmail(context.Background(), f.URL, "v", func(VerifyEvent) {})
	if err == nil {
		t.Fatal("no error")
	}
	cleanOf(t, "the 409 text", err.Error())
	if !strings.Contains(err.Error(), evilPlain) {
		t.Errorf("the 409 text %q lost the relay's printable words", err)
	}

	// The link, and a match code that is not a code, from a relay that sends
	// hostile ones.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/register/start":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"code":"fdv_c","verifyUrl":"https://relay.example/auth/verify#fdv_c` + evil + `",` +
				`"matchCode":"K7QM\u001b[2J42XD","expiresAt":"2030-01-01T00:30:00Z"}`))
		case "/api/v1/register/status":
			_, _ = w.Write([]byte(`{"verified":true,"expiresAt":"2030-01-01T00:30:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var first VerifyEvent
	seen := false
	if _, err := VerifyEmail(context.Background(), srv.URL, "v", func(ev VerifyEvent) {
		if ev.URL != "" && !seen {
			first, seen = ev, true
		}
	}); err != nil {
		t.Fatal(err)
	}
	cleanOf(t, "the verification URL", first.URL)
	cleanOf(t, "the event line", first.Line)
	if first.MatchCode != "" || strings.Contains(first.Line, "confirmation code") {
		t.Errorf("a match code that is not a code was shown: %+v", first)
	}
}

func TestPlansNamesAndLinksFromTheRelayAreCleaned(t *testing.T) {
	f := newFakeRelay(t)
	planJSON := `{"plan":"sub\u001b","name":"` + evil + `","active":true,"message":"` + evil + `","was":"` + evil + `"}`
	f.accountPreview = `{"desktops":1,"devices":1,"subscribed":false,"email":"j` + evil + `@example.com","plan":` + planJSON + `}`
	cfg := f.config()
	c := NewClient(&cfg, "v")

	p, err := c.AccountPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanOf(t, "preview email", p.Email)
	cleanOf(t, "preview plan name", p.Plan.Name)
	cleanOf(t, "preview plan message", p.Plan.Message)
	cleanOf(t, "preview plan", p.Plan.Plan+p.Plan.Was)
	if p.Plan.Name != evilPlain {
		t.Errorf("plan name = %q", p.Plan.Name)
	}

	// The registration answer's plan, as a join prints it.
	f.hostsReply = `{"hostId":"h2","accountId":"a9","token":"fdh_test","joined":true,"desktops":2,"plan":` + planJSON + `}`
	reg, err := Register(context.Background(), f.URL, "v", RegisterRequest{Name: "desk"})
	if err != nil {
		t.Fatal(err)
	}
	cleanOf(t, "registration plan name", reg.Plan.Name)
	cleanOf(t, "registration plan message", reg.Plan.Message)

	// Names and addresses in the roster, and a pairing link.
	f.accountPreview = ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/host/devices":
			_, _ = w.Write([]byte(`{"devices":[{"id":"d` + evil + `","name":"` + evil + `"}],` +
				`"hosts":[{"id":"h","name":"` + evil + `","self":true,"url":"https://x/h/` + evil + `"}],"plan":` + planJSON + `}`))
		case "/api/v1/host/pairings":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"code":"fdp_` + evil + `","url":"https://x/pair#` + evil + `","expiresAt":"2030-01-01T00:10:00Z"}`))
		}
	}))
	defer srv.Close()
	c2 := NewClient(&Config{Relay: srv.URL, Token: "fdh_t"}, "v")
	ro, err := c2.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanOf(t, "device id", ro.Devices[0].ID)
	cleanOf(t, "device name", ro.Devices[0].Name)
	cleanOf(t, "host name", ro.Hosts[0].Name)
	cleanOf(t, "host url", ro.Hosts[0].URL)
	cleanOf(t, "roster plan", ro.Plan.Name)
	pr, err := c2.Pair(context.Background(), KindDevice)
	if err != nil {
		t.Fatal(err)
	}
	cleanOf(t, "pairing code", pr.Code)
	cleanOf(t, "pairing url", pr.URL)
}
