package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A proxy's error page is shown as its title, not as kilobytes of markup, and
// a long body that is not JSON is clipped, on every wire.
func TestAnErrorPageIsShownAsItsTitle(t *testing.T) {
	page := "<!DOCTYPE html>\n<html lang=\"en-US\">\n<head>\n<title>example.com | 502: Bad gateway</title>\n" +
		"<style>" + strings.Repeat("body{margin:0}", 400) + "</style>\n</head>\n<body>" +
		strings.Repeat("<div class=\"cf-error\">Bad gateway</div>", 100) + "</body></html>"
	for _, c := range []struct{ name, body, want string }{
		{"a titled page", page, "example.com | 502: Bad gateway"},
		{"a page without a title", "<html><body>" + strings.Repeat("<p>down</p>", 500) + "</body></html>", "web page"},
		{"plain text", strings.Repeat("upstream connect error ", 400), "upstream connect error"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(c.body))
		}))
		for _, name := range []string{"anthropic", "openai", "gemini"} {
			wire, err := NewWire(name, srv.URL, "k")
			if err != nil {
				t.Fatal(err)
			}
			err = wire.Stream(context.Background(), Request{Model: "m"}, func(Event) {})
			if err == nil {
				t.Errorf("%s, %s: the 502 was not reported", c.name, name)
				continue
			}
			msg := err.Error()
			if !strings.Contains(msg, "502") || !strings.Contains(msg, c.want) || strings.Contains(msg, "<") || len(msg) > 400 {
				t.Errorf("%s, %s: error is %d bytes: %.600q", c.name, name, len(msg), msg)
			}
		}
		srv.Close()
	}
}
