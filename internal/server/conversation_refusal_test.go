package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The new shell's store tells a device's refusal of a conversation command by the
// start of the notice the desk sends a viewer. The phrase is written in store.js,
// so it is read from there and checked against what the server sends.
func TestNewShellStoreMatchesTheViewerRefusal(t *testing.T) {
	b, err := os.ReadFile("../webui/ui/assets/lib/store.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`export const VIEWER_REFUSED_PHRASE = "([^"]+)"`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("store.js does not declare VIEWER_REFUSED_PHRASE")
	}
	if !strings.HasPrefix(viewerRefused, m[1]) {
		t.Errorf("store.js matches the start %q, which is not the start of the refusal %q", m[1], viewerRefused)
	}
}

// The commands the conversation tab sends are the ones a viewer allowed to watch
// panes may send, and the ones it is answered with are the ones it is sent.
func TestConversationTabCommandsAreForWatchers(t *testing.T) {
	for _, c := range []string{"conversationOpen", "conversationOlder", "conversationClose"} {
		if commandAccess[c] != accessWatcher {
			t.Errorf("%s is not a watcher command", c)
		}
	}
	for _, m := range []string{"conversationPage", "conversationAppend"} {
		if !watcherMessageTypes[m] {
			t.Errorf("%s is not sent to a watcher", m)
		}
	}
}

// The new shell says the same line the server's refusal starts with.
func TestNewShellStoreMatchesTheRemoteConversationLine(t *testing.T) {
	b, err := os.ReadFile("../webui/ui/assets/lib/store.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`export const REMOTE_CONVERSATION_LINE = "([^"]+)"`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("store.js does not declare REMOTE_CONVERSATION_LINE")
	}
	if m[1] != conversationsOnDesktop {
		t.Errorf("store.js says %q, the server sends %q", m[1], conversationsOnDesktop)
	}
}
