package legacyenv

import (
	"bytes"
	"strings"
	"testing"
)

func TestOldNameStillWorksAndWarnsOnce(t *testing.T) {
	warned.Clear()
	t.Cleanup(warned.Clear)
	var buf bytes.Buffer
	t.Setenv("FLOCKDECK_TOKEN", "")
	t.Setenv("PERCH_TOKEN", "old-secret")

	for range 3 {
		if got := GetWarn(&buf, "TOKEN"); got != "old-secret" {
			t.Fatalf("GetWarn(TOKEN) = %q, want the PERCH_ value", got)
		}
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d warning lines, want 1: %q", len(lines), buf.String())
	}
	for _, want := range []string{"PERCH_TOKEN", "FLOCKDECK_TOKEN", RemovalDate, "will be removed"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("warning %q does not mention %q", lines[0], want)
		}
	}

	t.Setenv("FLOCKDECK_PANE", "")
	t.Setenv("PERCH_PANE", "p1")
	if got := GetWarn(&buf, "PANE"); got != "p1" {
		t.Errorf("GetWarn(PANE) = %q", got)
	}
	if n := strings.Count(buf.String(), "\n"); n != 2 {
		t.Errorf("got %d warning lines, want 2: %q", n, buf.String())
	}
}

func TestGetNeverWarns(t *testing.T) {
	warned.Clear()
	t.Cleanup(warned.Clear)
	t.Setenv("FLOCKDECK_TOKEN", "")
	t.Setenv("PERCH_TOKEN", "old-secret")
	if got := Get("TOKEN"); got != "old-secret" {
		t.Errorf("Get(TOKEN) = %q, want the PERCH_ value", got)
	}
	if _, seen := warned.Load("TOKEN"); seen {
		t.Error("Get recorded a warning")
	}
}

func TestCurrentNameWinsAndSaysNothing(t *testing.T) {
	warned.Clear()
	t.Cleanup(warned.Clear)
	var buf bytes.Buffer
	t.Setenv("FLOCKDECK_API", "http://127.0.0.1:1")
	t.Setenv("PERCH_API", "http://127.0.0.1:2")
	if got := GetWarn(&buf, "API"); got != "http://127.0.0.1:1" {
		t.Errorf("GetWarn(API) = %q, want the FLOCKDECK_ value", got)
	}
	if buf.Len() != 0 {
		t.Errorf("warned although the current name is set: %q", buf.String())
	}
}
