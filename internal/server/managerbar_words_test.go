package server

import (
	"os"
	"regexp"
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// The manager bar stops an empty text and one over 16 KiB in the page, in the
// desk's words. The words are written in ManagerBar.js, so they are read from
// there and checked against what the workspace says.
func TestManagerBarUsesTheDesksWords(t *testing.T) {
	b, err := os.ReadFile("../webui/next/assets/graph/ManagerBar.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for name, want := range map[string]string{
		"EMPTY_WORDS":    workspace.ErrSendEmpty.Error(),
		"TOO_LONG_WORDS": workspace.ErrSendTooLong.Error(),
	} {
		m := regexp.MustCompile(`export const ` + name + ` = "([^"]+)"`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("ManagerBar.js does not declare %s", name)
		}
		if m[1] != want {
			t.Errorf("%s is %q, the workspace says %q", name, m[1], want)
		}
	}
	if m := regexp.MustCompile(`export const MAX_BYTES = (\d+) \* (\d+)`).FindStringSubmatch(src); m == nil || m[1]+"*"+m[2] != "16*1024" || workspace.MaxPaneSendBytes != 16*1024 {
		t.Errorf("MAX_BYTES in ManagerBar.js does not match workspace.MaxPaneSendBytes")
	}
}
