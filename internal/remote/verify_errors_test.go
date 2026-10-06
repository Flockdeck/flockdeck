package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What a person sees when the relay refuses to start a registration: words they can act on,
// different for each case, and neither the status nor the relay's JSON. The email address is
// typed on the relay's page, so 422 and 502 are not among them.
func TestStartRefusalsAreSaidInPlainWords(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   []string
	}{
		{http.StatusInternalServerError, `{"error":"db: connection lost"}`, []string{"Try again later"}},
		{http.StatusInternalServerError, ``, []string{"Try again later"}},
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
		for _, raw := range []string{"{", "db:", "slow down", "answered", "Internal Server", "the relay said"} {
			if strings.Contains(msg, raw) {
				t.Errorf("%d: %q shows %q, which is raw", tc.status, msg, raw)
			}
		}
		var api *APIError
		if !errors.As(err, &api) || api.Status != tc.status {
			t.Errorf("%d: the status is not kept for errors.As: %v", tc.status, err)
		}
	}
	if a, b := verifyError(&APIError{Status: 429}).Error(), verifyError(&APIError{Status: 500}).Error(); a == b {
		t.Error("429 and 500 are said the same way")
	}
}

// Another status is left as it was: a 422 or 502 is not something this machine is sent.
func TestVerificationLeavesOtherErrorsAlone(t *testing.T) {
	for _, status := range []int{http.StatusUnprocessableEntity, http.StatusBadGateway} {
		in := &APIError{Status: status}
		if got := verifyError(in); got != error(in) {
			t.Errorf("verifyError(%d) = %v", status, got)
		}
	}
	plain := errors.New("reach the relay: no answer")
	if got := verifyError(plain); got != plain {
		t.Errorf("verifyError(plain) = %v", got)
	}
}
