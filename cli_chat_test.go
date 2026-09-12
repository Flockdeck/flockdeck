package main

import (
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/internal/chat"
)

// A pane is started with only its agent's id, and the chat takes the wire, the
// address and the key variables from that agent's catalog entry -- rather than
// speaking the Anthropic wire to Anthropic for every agent there is.
func TestTheChatTakesItsEndpointFromTheAgentsCatalogEntry(t *testing.T) {
	isolateKeys(t)

	// An address given to the local agent -- here with `flockdeck keys
	// endpoint`, as easily in agents.json -- is the one its chat talks to.
	if _, err := runKeysCmd(t, "", "endpoint", "openai-compatible", "http://127.0.0.1:11434/v1"); err != nil {
		t.Fatal(err)
	}
	local := chat.Options{Agent: "openai-compatible"}
	fillFromCatalog(&local)
	if local.Wire != "openai" || local.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Errorf("the local agent's chat got wire %q at %q", local.Wire, local.BaseURL)
	}

	openai := chat.Options{Agent: "openai"}
	fillFromCatalog(&openai)
	if openai.Wire != "openai" || !slices.Contains(openai.KeyEnv, "OPENAI_API_KEY") {
		t.Errorf("the OpenAI agent's chat got wire %q and key variables %q", openai.Wire, openai.KeyEnv)
	}

	// What the command line or the environment says is kept.
	told := chat.Options{Agent: "openai", Wire: "gemini", BaseURL: "https://gateway.example/v1"}
	fillFromCatalog(&told)
	if told.Wire != "gemini" || told.BaseURL != "https://gateway.example/v1" {
		t.Errorf("an explicit wire and address were overwritten: %q at %q", told.Wire, told.BaseURL)
	}

	// An address changed with `flockdeck keys endpoint` reaches the next pane.
	if _, err := runKeysCmd(t, "", "endpoint", "openai-compatible", "http://127.0.0.1:9999/v1"); err != nil {
		t.Fatal(err)
	}
	moved := chat.Options{Agent: "openai-compatible"}
	fillFromCatalog(&moved)
	if moved.BaseURL != "http://127.0.0.1:9999/v1" {
		t.Errorf("the changed address did not reach the chat: %q", moved.BaseURL)
	}
}
