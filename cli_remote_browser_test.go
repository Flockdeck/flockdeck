package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/remote"
)

func TestVerifyEventPrinterBrowser(t *testing.T) {
	const link = "http://127.0.0.1:1/auth/verify#fdv_test"
	cases := []struct {
		name    string
		openErr error
		want    string
		notWant string
	}{
		{"opened", nil, "Opened in your default browser", "Open this link in a browser"},
		{"open fails", errors.New("no browser"), "Open this link in a browser", "Opened in your default browser"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opened := stubBrowser(t, c.openErr)
			var out bytes.Buffer
			verifyEventPrinter(&out)(remote.VerifyEvent{URL: link})

			if len(*opened) != 1 || (*opened)[0] != link {
				t.Fatalf("opener calls = %v, want exactly [%s]", *opened, link)
			}
			got := out.String()
			if !strings.Contains(got, c.want) {
				t.Errorf("output lacks %q:\n%s", c.want, got)
			}
			if strings.Contains(got, c.notWant) {
				t.Errorf("output has %q:\n%s", c.notWant, got)
			}
			if !strings.Contains(got, link) {
				t.Errorf("output lacks the URL:\n%s", got)
			}
		})
	}
}

// Events that carry no URL must not open anything.
func TestVerifyEventPrinterOpensOnlyForURL(t *testing.T) {
	opened := stubBrowser(t, nil)
	var out bytes.Buffer
	print := verifyEventPrinter(&out)
	print(remote.VerifyEvent{Line: "hello"})
	print(remote.VerifyEvent{Done: true})
	if len(*opened) != 0 {
		t.Errorf("opener called for URL-less events: %v", *opened)
	}
}
