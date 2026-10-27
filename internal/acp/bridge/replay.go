package bridge

import (
	"crypto/sha256"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// BeginReplay is called before session/load, which makes the agent send the
// conversation so far as updates. It empties the transcript, so that the replay
// is the only copy of the history, and from then until EndReplay the updates
// the client marks as replay (acp.Update.Replay) are taken in: the person's
// words as prompt entries, the agent's as replies, thinking and tools. A replay
// never starts a turn: the status stays idle and no prompt is sent.
//
// A replay update that arrives outside that window is dropped, so a second
// replay cannot double the transcript.
func (b *Bridge) BeginReplay() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetItems()
	b.replaying, b.liveSeen = true, false
}

// EndReplay ends the replay, whether session/load succeeded or not. A tool the
// replay left running is settled: its turn ended with the old session, so it is
// shown with no status rather than as running.
func (b *Bridge) EndReplay() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.replaying = false
	b.closeMerges()
	for _, it := range b.items {
		if it.e.Kind == transcript.KindTool && it.e.Status == transcript.StatusRunning {
			it.e.Status = ""
			it.dirty = true
		}
	}
}

// DropReplay empties the transcript and ends the replay: session/load failed
// part-way, and what came before it is not a conversation to show.
func (b *Bridge) DropReplay() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.replaying = false
	b.resetItems()
}

// Notice adds a notice entry to the conversation and returns what is due to be
// handed out.
func (b *Bridge) Notice(text string) []transcript.Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeMerges()
	b.add(transcript.Entry{Kind: transcript.KindNotice, Text: line(text, labelCap)})
	b.enforce()
	return b.take(true)
}

// resetItems forgets every entry. The entry counter keeps going, so an id is
// never used twice. Call with b.mu held.
func (b *Bridge) resetItems() {
	b.items = nil
	b.tools = map[[sha256.Size]byte]*item{}
	b.mails = nil
	b.detail, b.inline, b.detailFrom = 0, 0, b.seq
	b.inFlight = false
}

// replayUpdate folds an update that is part of a replay. Call with b.mu held.
func (b *Bridge) replayUpdate(u acp.Update) {
	if !b.replaying {
		return
	}
	switch u.Kind {
	case acp.UpdateUserMessageChunk:
		b.chunk(u, transcript.KindPrompt)
	case acp.UpdateAgentMessageChunk:
		b.chunk(u, transcript.KindReply)
	case acp.UpdateAgentThoughtChunk:
		b.chunk(u, transcript.KindThinking)
	case acp.UpdateToolCall, acp.UpdateToolCallUpdate:
		b.tool(u)
	default:
		// Session state sent during a replay is the state now, and is kept.
		b.update(u)
	}
	b.enforce()
	b.trimReplay()
}

// trimReplay keeps a replay inside the entry and text limits. enforce never
// drops a prompt, which is right for a conversation the person typed, but a
// replay is the agent's to make as long as it likes: here the oldest entries go
// first, prompts too. Call with b.mu held.
func (b *Bridge) trimReplay() {
	max, budget := b.cfg.MaxEntries, b.cfg.TextBudget
	if len(b.items) <= max && b.inline <= budget {
		return
	}
	n := 0
	for ; n < len(b.items)-1 && (len(b.items)-n > max-max/10 || b.inline > budget-budget/10); n++ {
		it := b.items[n]
		b.detail -= it.detail
		b.inline -= it.inl
		if it.keyed {
			delete(b.tools, it.key)
		}
		if it.e.Kind == transcript.KindMessage {
			delete(b.mails, mailKey{it.e.MsgID, it.e.Dir})
		}
		b.dropped++
	}
	b.items = b.items[n:]
}
