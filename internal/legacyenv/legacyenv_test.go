package legacyenv

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	warned.Clear()
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		warned.Clear()
	})
	return &buf
}

func TestOldNameStillWorksAndWarnsOnce(t *testing.T) {
	buf := capture(t)
	t.Setenv("FLOCKDECK_TOKEN", "")
	t.Setenv("PERCH_TOKEN", "old-secret")

	for range 3 {
		if got := Get("TOKEN"); got != "old-secret" {
			t.Fatalf("Get(TOKEN) = %q, want the PERCH_ value", got)
		}
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d warning lines, want 1: %q", len(lines), buf.String())
	}
	for _, want := range []string{"PERCH_TOKEN", "FLOCKDECK_TOKEN", RemovalDate} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("warning %q does not mention %q", lines[0], want)
		}
	}

	// Another name is warned about in its own right.
	t.Setenv("FLOCKDECK_PANE", "")
	t.Setenv("PERCH_PANE", "p1")
	if got := Get("PANE"); got != "p1" {
		t.Errorf("Get(PANE) = %q", got)
	}
	if n := strings.Count(buf.String(), "\n"); n != 2 {
		t.Errorf("got %d warning lines, want 2: %q", n, buf.String())
	}
}

func TestCurrentNameWinsAndSaysNothing(t *testing.T) {
	buf := capture(t)
	t.Setenv("FLOCKDECK_API", "http://127.0.0.1:1")
	t.Setenv("PERCH_API", "http://127.0.0.1:2")
	if got := Get("API"); got != "http://127.0.0.1:1" {
		t.Errorf("Get(API) = %q, want the FLOCKDECK_ value", got)
	}
	if buf.Len() != 0 {
		t.Errorf("warned although the current name is set: %q", buf.String())
	}
}

func TestNeitherNameSetSaysNothing(t *testing.T) {
	buf := capture(t)
	t.Setenv("FLOCKDECK_PROJECT", "")
	t.Setenv("PERCH_PROJECT", "")
	if got := Get("PROJECT"); got != "" || buf.Len() != 0 {
		t.Errorf("Get(PROJECT) = %q, log %q; want neither", got, buf.String())
	}
}
