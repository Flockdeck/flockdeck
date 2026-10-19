package hooks

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The SendMessage tap reads one thing from a Claude session's tool call: who it
// sent a message to. It never keeps the message.
//
// What the tool's input looks like has not been confirmed against a real session
// (see the shape table below), so the parser accepts the likely names and a call
// it cannot read is counted rather than guessed at. A wrong table then shows as
// calls seen and not understood, and nothing is drawn that was not read.

// sendMessageTools are the tool names taken for a SendMessage call, compared in
// lower case.
var sendMessageTools = []string{"sendmessage", "send_message"}

// sendMessageRecipient are the input fields that may name the recipient, in the
// order they are tried. sendMessageText are the fields that may carry the
// message. A call is understood only when one of each is present and not empty.
var (
	sendMessageRecipient = []string{"to", "recipient", "recipient_name", "target", "agent", "agent_name", "receiver", "name"}
	sendMessageText      = []string{"message", "text", "content", "body", "summary", "prompt"}
)

// sendMessageEveryone are recipient values that address everyone, which is no
// single pane.
var sendMessageEveryone = []string{"*", "all", "everyone", "broadcast"}

// IsSendMessage reports whether tool is taken for a SendMessage call.
func IsSendMessage(tool string) bool {
	t := strings.ToLower(strings.TrimSpace(tool))
	for _, n := range sendMessageTools {
		if t == n {
			return true
		}
	}
	return false
}

// SendMessageRecipient reads the recipient from the input of a SendMessage call,
// as the JSON the hook reported it. ok is false when the input has no recipient
// and no message in a shape the table knows, and then to is empty.
//
// The recipient is a name as the sender wrote it, the way ListAgents shows it.
// The text of the message is checked for and dropped.
func SendMessageRecipient(input json.RawMessage) (to string, ok bool) {
	input = bytes.TrimSpace(input)
	if len(input) == 0 || input[0] != '{' {
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return "", false
	}
	// Field names are matched without regard to case.
	lower := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		k = strings.ToLower(k)
		if _, dup := lower[k]; dup {
			// Two keys that differ only in case: which one the sender meant is
			// not something to guess at, and a map's order is not stable.
			return "", false
		}
		lower[k] = v
	}
	hasText := false
	for _, k := range sendMessageText {
		raw := bytes.TrimSpace(lower[k])
		if s, ok := textOf(raw); ok && s != "" {
			hasText = true
			break
		}
		// A structured message is an object or an array that is not empty.
		if len(raw) > 2 && (raw[0] == '{' || raw[0] == '[') {
			hasText = true
			break
		}
	}
	if !hasText {
		return "", false
	}
	for _, k := range sendMessageRecipient {
		raw, present := lower[k]
		if !present {
			continue
		}
		name, ok := recipientOf(raw)
		if !ok {
			continue
		}
		for _, e := range sendMessageEveryone {
			if strings.EqualFold(name, e) {
				return "", false
			}
		}
		return name, true
	}
	return "", false
}

// textOf reads a JSON string, trimmed.
func textOf(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return strings.TrimSpace(s), true
}

// recipientOf reads a recipient that is a string, or an object with a name or id
// in it.
func recipientOf(raw json.RawMessage) (string, bool) {
	if s, ok := textOf(raw); ok {
		return s, s != "" && len(s) <= maxRecipient
	}
	var o struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return "", false
	}
	for _, s := range []string{o.Name, o.ID} {
		if s = strings.TrimSpace(s); s != "" && len(s) <= maxRecipient {
			return s, true
		}
	}
	return "", false
}

// maxRecipient is the longest recipient name taken for one.
const maxRecipient = 200
