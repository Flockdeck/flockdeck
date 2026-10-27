package bridge

import (
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Mail entries. A message between agents is its own kind of entry, never a
// prompt: the person's turns are recorded by TurnStart and nothing an agent or a
// peer writes can be recorded as one. The body, the peer's label and the state
// are cleaned here again whatever the caller did, because they are text some
// other agent wrote.

// maxMailText bounds the body kept on a message entry. The inbox itself holds
// at most 8 KiB, so this is only a guard.
const maxMailText = 16 << 10

// mailKey names a message entry: the id of the message in the inbox and the
// side of it this conversation is on. The same id is in the sender's
// conversation as "out" and the receiver's as "in".
type mailKey struct {
	id  int
	dir string
}

// Mail is one message as the bridge shows it.
type Mail struct {
	// ID is the message's id in the team inbox. It must be positive.
	ID int
	// Peer is the label of the other side: the sender of mail received, the
	// recipient of mail sent.
	Peer string
	// Text is the body, as plain text.
	Text string
	// State is the delivery state: queued, delivered, handled or undeliverable.
	State string
}

// mailState reports whether s is a delivery state a message entry may carry.
func mailState(s string) bool {
	switch s {
	case "queued", "delivered", "handled", "undeliverable":
		return true
	}
	return false
}

// mailEntry makes the entry for m on the side dir.
func mailEntry(dir string, m Mail) transcript.Entry {
	state := m.State
	if !mailState(state) {
		state = "queued"
	}
	return transcript.Entry{
		Kind: transcript.KindMessage, Dir: dir, MsgID: m.ID, Delivery: state,
		Peer: line(m.Peer, labelCap),
		Text: clean(capBytes(m.Text, maxMailText)),
	}
}

// MailReceived records the mail the prompt just started carries and marks the
// turn in flight, as TurnStart does for the person's prompt. The entry is a
// message from its sender and not a prompt. A message already shown (its turn
// is being run again) keeps its entry and takes the new state. The returned
// entries are for the caller to hand on at once.
func (b *Bridge) MailReceived(m Mail) []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeMerges()
	b.mail(transcript.MessageIn, m)
	b.inFlight = true
	b.enforce()
	return b.take(true)
}

// MailSent records mail this session sent, without touching the turn.
func (b *Bridge) MailSent(m Mail) []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeMerges()
	b.mail(transcript.MessageOut, m)
	b.enforce()
	return b.take(true)
}

func (b *Bridge) mail(dir string, m Mail) {
	if m.ID <= 0 {
		return
	}
	k := mailKey{m.ID, dir}
	if it, ok := b.mails[k]; ok {
		b.setMailState(it, m.State)
		return
	}
	it := b.add(mailEntry(dir, m))
	if b.mails == nil {
		b.mails = map[mailKey]*item{}
	}
	b.mails[k] = it
}

func (b *Bridge) setMailState(it *item, state string) {
	if !mailState(state) || it.e.Delivery == state {
		return
	}
	it.e.Delivery = state
	it.dirty, it.urgent = true, true
}

// MailState moves the message entries of message id, on whichever side this
// conversation holds, to state. It reports whether there was one. A state that
// is not one of the four is ignored.
func (b *Bridge) MailState(id int, state string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	found := false
	for _, dir := range []string{transcript.MessageIn, transcript.MessageOut} {
		if it, ok := b.mails[mailKey{id, dir}]; ok {
			b.setMailState(it, state)
			found = true
		}
	}
	return found
}
