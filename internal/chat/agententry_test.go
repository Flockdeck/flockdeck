package chat

import (
	"path/filepath"
	"testing"
)

// The chats of every API agent are kept in one folder, so each says which
// agent it was held with: a conversation with the OpenAI agent must reopen as
// that agent, not as whichever API agent comes first.
func TestAConversationRecordsItsAgent(t *testing.T) {
	dir := t.TempDir()
	run(t, Options{Agent: "openai", Dir: dir, Task: "hello"}, "", &scriptedWire{turns: []turnFunc{says("hi")}})

	entries, err := ReadEntries(filepath.Join(dir, "session-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was recorded")
	}
	for _, e := range entries {
		if e.Agent != "openai" {
			t.Errorf("entry %+v does not say it was the openai agent's", e)
		}
	}
}
