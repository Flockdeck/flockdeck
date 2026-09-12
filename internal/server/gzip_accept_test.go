package server

import (
	"net/http/httptest"
	"testing"
)

// TestAcceptsGzipReadsARefusal covers the header a relayed request says what it
// can take in. A coding given a quality of zero is one the client refuses, and
// "gzip;q=0" was read as an offer because only the name before the semicolon
// was looked at.
func TestAcceptsGzipReadsARefusal(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"gzip", true},
		{"gzip, deflate, br", true},
		{"br, GZIP", true},
		{"gzip;q=0.5", true},
		{"gzip; q=1", true},
		{"", false},
		{"deflate, br", false},
		{"gzip;q=0", false},
		{"gzip; q=0.000", false},
		{"br, gzip;Q=0", false},
	} {
		r := httptest.NewRequest("GET", "/assets/app.js", nil)
		if tc.header != "" {
			r.Header.Set("Accept-Encoding", tc.header)
		}
		if got := acceptsGzip(r); got != tc.want {
			t.Errorf("Accept-Encoding %q: acceptsGzip = %v, want %v", tc.header, got, tc.want)
		}
	}
}
