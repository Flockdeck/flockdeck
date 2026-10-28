package structuredtest

import (
	"errors"
	"fmt"
	"sync"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// Memory is a pane that keeps one card, the way the real adapter is meant to:
// every call takes one lock, the answer is checked and taken in it, and nothing
// in it waits for anything else. It is the reference the conformance test is
// proved against, and what the server's tests use as a strict pane.
type Memory struct {
	// ErrStale is the error returned for a request no longer waiting; set it to
	// server.ErrPermissionStale.
	ErrStale error
	// Tool and Command go on the cards it opens.
	Tool, Command string
	// Own is what OwnAdapter says. Token is what RuntimeToken says; empty is "memory".
	Own   bool
	Token string

	mu       sync.Mutex
	seq      uint64
	card     *bridge.Card
	closed   bool
	answered []Answer
}

// Open makes a card pending, replacing any other, and gives it the next Seq.
func (m *Memory) Open(requestID string, options []acp.PermissionOption) bridge.Card {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	c := bridge.Card{Seq: m.seq, RequestID: requestID, SessionID: "s1", Tool: m.Tool, Kind: "execute", Command: m.Command,
		Options: append([]acp.PermissionOption(nil), options...)}
	if !m.closed {
		m.card = &c
	}
	return c
}

// End drops the card, as the agent ending its request does.
func (m *Memory) End(card bridge.Card) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.card != nil && m.card.Seq == card.Seq {
		m.card = nil
	}
}

// Close ends the pane.
func (m *Memory) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed, m.card = true, nil
}

// PendingPermission is the card waiting, or nil.
func (m *Memory) PendingPermission() *bridge.Card {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.card == nil {
		return nil
	}
	c := *m.card
	c.Options = append([]acp.PermissionOption(nil), c.Options...)
	return &c
}

// AnswerPermission takes the answer if the card with this Seq and RequestID is
// the one waiting and the option is on it.
func (m *Memory) AnswerPermission(seq uint64, requestID, optionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.card == nil || m.card.Seq != seq || m.card.RequestID != requestID {
		return fmt.Errorf("answer for card %d: %w", seq, m.ErrStale)
	}
	for _, o := range m.card.Options {
		if o.OptionID == optionID {
			m.answered = append(m.answered, Answer{seq, requestID, optionID})
			m.card = nil
			return nil
		}
	}
	return errors.New("that is not one of the options offered")
}

// OwnAdapter says whether the pane stands for one of Flockdeck's own adapters.
func (m *Memory) OwnAdapter() bool { return m.Own }

// RuntimeToken is Token, or "memory".
func (m *Memory) RuntimeToken() string {
	if m.Token == "" {
		return "memory"
	}
	return m.Token
}

// Answered is every answer taken.
func (m *Memory) Answered() []Answer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Answer(nil), m.answered...)
}

// Harness drives m.
func (m *Memory) Harness() Harness {
	return Harness{Pane: m, Open: m.Open, Answered: m.Answered, Close: m.Close, End: m.End}
}
