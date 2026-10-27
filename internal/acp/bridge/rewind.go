package bridge

import "github.com/jmwri/flockdeck/internal/session/transcript"

// TruncateBefore takes the prompt entry id, and every entry after it, out of
// the conversation: the agent was taken back to before that prompt. It returns
// the prompt's text, so that the person can send it again, edited or not. It
// returns false and changes nothing when id is not a prompt entry the bridge
// still holds.
//
// The entry counter keeps going, so an id that was dropped is never used again
// and a window holding it matches nothing new. Tools the dropped entries held
// are forgotten with them, and a later update for one of them starts a new
// entry as for any tool call the bridge has not seen.
func (b *Bridge) TruncateBefore(id string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	at := -1
	for i, it := range b.items {
		if it.e.ID == id && it.e.Kind == transcript.KindPrompt {
			at = i
			break
		}
	}
	if at < 0 {
		return "", false
	}
	text := b.items[at].e.Text
	for _, it := range b.items[at:] {
		b.detail -= it.detail
		b.inline -= it.inl
		if it.keyed {
			delete(b.tools, it.key)
		}
		if it.e.Kind == transcript.KindMessage {
			delete(b.mails, mailKey{it.e.MsgID, it.e.Dir})
		}
	}
	b.items = b.items[:at:at]
	b.inFlight = false
	return text, true
}
