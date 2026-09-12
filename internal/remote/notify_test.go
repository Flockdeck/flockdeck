package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Notify posts the notification as the desktop, and hands back what the relay
// said: how many devices were sent it, or the relay's own refusal, which is
// not taken for this machine's credentials being spent.
func TestNotifyAsksTheRelayToPush(t *testing.T) {
	var got Notification
	var auth string
	refuse := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/host/notify" {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		if refuse {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":"Your remote access trial has ended."}`))
			return
		}
		_, _ = w.Write([]byte(`{"sent":2}`))
	}))
	defer srv.Close()

	c := &Client{Relay: srv.URL, Token: "fdh_test"}
	n := Notification{PaneID: "p1", Title: "claude · api needs you", Body: "On desk"}
	sent, err := c.Notify(context.Background(), n)
	if err != nil || sent != 2 {
		t.Fatalf("Notify = %d, %v", sent, err)
	}
	if got != n || auth != "Bearer fdh_test" {
		t.Errorf("the relay was sent %+v with %q", got, auth)
	}

	refuse = true
	_, err = c.Notify(context.Background(), n)
	if err == nil || err.Error() != "the relay said: Your remote access trial has ended." {
		t.Errorf("a refusal: %v", err)
	}
	if IsRevoked(err) {
		t.Error("a plan's refusal was taken for this machine's credentials being spent")
	}
}
