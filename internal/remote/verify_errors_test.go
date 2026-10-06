package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What a person sees when the relay refuses the verification step: words they can act on,
// different for each case, and neither the status nor the relay's JSON.
func TestVerificationRefusalsAreSaidInPlainWords(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   []string
	}{
		{http.StatusUnprocessableEntity, `{"error":"postmark: InactiveRecipient (406)"}`, []string{"Use another address", "privacy@flockdeck.ai"}},
		{http.StatusBadGateway, `{"error":"postmark: 500"}`, []string{"try again later"}},
		{http.StatusBadGateway, ``, []string{"try again later"}},
		{http.StatusTooManyRequests, `{"error":"slow down"}`, []string{"Wait a while"}},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		_, err := VerifyEmail(context.Background(), srv.URL, "v", func(VerifyEvent) {})
		srv.Close()
		if err == nil {
			t.Fatalf("%d: no error", tc.status)
		}
		msg := err.Error()
		for _, w := range tc.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%d: %q does not say %q", tc.status, msg, w)
			}
		}
		for _, raw := range []string{"{", "postmark", "slow down", "answered", "Unprocessable", "Bad Gateway", "the relay said"} {
			if strings.Contains(msg, raw) {
				t.Errorf("%d: %q shows %q, which is raw", tc.status, msg, raw)
			}
		}
		var api *APIError
		if !errors.As(err, &api) || api.Status != tc.status {
			t.Errorf("%d: the status is not kept for errors.As: %v", tc.status, err)
		}
	}
	if a, b := verifyError(&APIError{Status: 422}).Error(), verifyError(&APIError{Status: 502}).Error(); a == b {
		t.Error("422 and 502 are said the same way")
	}
}

// Another status is left as it was, so a 500 or a bad address still says what it did.
func TestVerificationLeavesOtherErrorsAlone(t *testing.T) {
	in := &APIError{Status: http.StatusInternalServerError}
	if got := verifyError(in); got != error(in) {
		t.Errorf("verifyError(500) = %v", got)
	}
	plain := errors.New("reach the relay: no answer")
	if got := verifyError(plain); got != plain {
		t.Errorf("verifyError(plain) = %v", got)
	}
}

// A relay that turns the address down while the desktop waits is not waited on to the end.
func TestVerifyEmailStopsWhenTheAddressIsRejectedWhileWaiting(t *testing.T) {
	quickPoll(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/register/start" {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"code":"c","verifyUrl":"https://relay.example/v#c","expiresAt":"2999-01-01T00:00:00Z"}`))
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	var last VerifyEvent
	_, err := VerifyEmail(context.Background(), srv.URL, "v", func(e VerifyEvent) { last = e })
	if err == nil || !strings.Contains(err.Error(), "Use another address") {
		t.Fatalf("VerifyEmail = %v, want the address refusal", err)
	}
	if last.Err == nil {
		t.Error("the last event carries no error")
	}
}

// Registering with a verification code the relay then refuses is said the same way.
func TestRegisterWithACodeSaysAnEmailProviderFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := Register(context.Background(), srv.URL, "v", RegisterRequest{Name: "n", VerificationCode: "c"})
	if err == nil || !strings.Contains(err.Error(), "try again later") {
		t.Errorf("Register = %v", err)
	}
	// Without a code the 502 is the ordinary one.
	_, err = Register(context.Background(), srv.URL, "v", RegisterRequest{Name: "n"})
	if err == nil || strings.Contains(err.Error(), "email provider") {
		t.Errorf("Register without a code = %v", err)
	}
}
