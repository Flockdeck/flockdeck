package chat

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// An endpoint whose certificate this machine does not trust is said to be one,
// with the usual cause, rather than in the words of Go's TLS stack.
func TestAnUntrustedCertificateIsSaidInWords(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request was answered over a connection that should not have been trusted")
	}))
	defer srv.Close()
	wire, err := NewWire("openai", srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	out := run(t, Options{Agent: "openai", Model: "m", BaseURL: srv.URL}, "hello\n/exit\n", wire)
	said := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(said, "the endpoint's certificate is not trusted on this machine") ||
		!strings.Contains(said, "a proxy or antivirus inspecting HTTPS is the usual cause") || strings.Contains(said, "x509") {
		t.Errorf("the certificate was not said in words:\n%s", out)
	}
}

// A proxy that is not answering is named as the proxy, not as the endpoint
// behind it, which was never reached at all.
func TestAProxyThatIsNotAnsweringIsNamed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	saved := httpClient
	t.Cleanup(func() { httpClient = saved })
	httpClient = &http.Client{
		Transport:     &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: addr})},
		CheckRedirect: keepToTheAddress,
	}

	base := "https://api.example.com/v1"
	wire, err := NewWire("openai", base, "k")
	if err != nil {
		t.Fatal(err)
	}
	out := run(t, Options{Agent: "openai", Model: "m", BaseURL: base}, "hello\n/exit\n", wire)
	said := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(said, "could not reach the proxy at "+addr+" (HTTPS_PROXY); is it running?") ||
		strings.Contains(said, "proxyconnect") || strings.Contains(said, "could not reach the endpoint") {
		t.Errorf("the proxy was not named:\n%s", out)
	}
}
