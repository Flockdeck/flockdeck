package server

import (
	"strings"
	"unicode"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// This file is the desktop side of the phone's search-a-conversation flow:
// the conversationSearch command and its reply. See conversation.go for the
// stream it searches, and the scratchpad's convo-protocol.md for the wire
// contract.

// conversationSearchMaxMatches caps how many hits an answer carries -- newest
// first, per the protocol -- with More said once a search finds more than
// this many.
const conversationSearchMaxMatches = 50

// conversationSearchMaxQueryRunes bounds how much of a query is ever searched
// for, against a phone sending something far longer than anyone would type
// into a search box.
const conversationSearchMaxQueryRunes = 200

// conversationSearchSnippetRadius is how many runes either side of a hit its
// snippet carries, for a snippet of roughly 120 characters end to end.
const conversationSearchSnippetRadius = 60

// conversationSearchMsg answers a conversationSearch command.
type conversationSearchMsg struct {
	Type    string                  `json:"type"`
	ID      string                  `json:"id"`
	Q       string                  `json:"q"`
	Matches []conversationSearchHit `json:"matches"`
	More    bool                    `json:"more"`
}

// conversationSearchHit is one matching entry: its id and kind (for the phone
// to draw the right icon and to jump to it, per conversationOlder), a snippet
// of the text the hit sits in, and where in that snippet the hit starts, in
// runes, for the phone to highlight it.
type conversationSearchHit struct {
	EntryID string `json:"entryId"`
	Kind    string `json:"kind"`
	Snippet string `json:"snippet"`
	Offset  int    `json:"offset"`
}

// conversationSearch answers a phone's search of one pane's whole
// conversation: a plain-text, case-insensitive substring search -- never a
// regular expression, so a query like ".*" matches only that literal text --
// over the text a person actually reads: a prompt, a reply, or a tool's own
// label and summary. Thinking text and a tool's full output are never
// searched, because they are never held anywhere but the stream's own
// detail-on-demand cache, fetched only when a row is expanded.
//
// It searches only the pane's already-retained stream -- built, and kept
// full, because some client has this pane open, or has had it open before
// (see conversation.go's syncStreamLocked and SetLight). A pane nobody has
// ever opened is answered empty rather than promoted to a full stream just to
// search it: search is a convenience for a conversation already open
// somewhere, not a reason to start retaining one that otherwise costs
// nothing.
func (s *Server) conversationSearch(c *controlClient, paneID, q string) {
	q = strings.TrimSpace(q)
	if r := []rune(q); len(r) > conversationSearchMaxQueryRunes {
		q = string(r[:conversationSearchMaxQueryRunes])
	}
	if paneID == "" || q == "" {
		c.sendJSON(conversationSearchMsg{Type: "conversationSearch", ID: paneID, Q: q})
		return
	}
	pc, ok := s.convos.lookup(paneID)
	if !ok {
		c.sendJSON(conversationSearchMsg{Type: "conversationSearch", ID: paneID, Q: q})
		return
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if !pc.supported || pc.stream == nil {
		c.sendJSON(conversationSearchMsg{Type: "conversationSearch", ID: paneID, Q: q})
		return
	}

	snapshot := pc.stream.Snapshot()
	var hits []conversationSearchHit
	more := false
	for i := len(snapshot) - 1; i >= 0; i-- {
		snippet, offset, found := matchEntryText(snapshot[i], q)
		if !found {
			continue
		}
		if len(hits) >= conversationSearchMaxMatches {
			more = true
			break
		}
		hits = append(hits, conversationSearchHit{
			EntryID: snapshot[i].ID, Kind: snapshot[i].Kind, Snippet: snippet, Offset: offset,
		})
	}
	c.sendJSON(conversationSearchMsg{Type: "conversationSearch", ID: paneID, Q: q, Matches: hits, More: more})
}

// matchEntryText picks the text a person reads for e's kind and looks for q
// in it -- a prompt's own words, a reply's markdown, or a tool's label and
// summary together (the one-line, always-inline parts of a tool row; never
// its diff or its full output, which are not "text a person reads" without
// tapping to expand them). Every other kind is left out of search entirely,
// per the design: thinking above all, since its text is never held outside
// the detail cache, but also subagent, compaction, notice and image, which
// carry nothing this cheap a search should reach into.
func matchEntryText(e transcript.Entry, q string) (snippet string, offset int, ok bool) {
	switch e.Kind {
	case transcript.KindPrompt:
		return findSnippet(e.Text, q)
	case transcript.KindReply:
		return findSnippet(e.Markdown, q)
	case transcript.KindTool:
		text := e.Label
		if e.Summary != "" {
			if text != "" {
				text += " " + e.Summary
			} else {
				text = e.Summary
			}
		}
		return findSnippet(text, q)
	default:
		return "", 0, false
	}
}

// findSnippet looks for q in haystack, case-insensitively and as plain text,
// and returns roughly conversationSearchSnippetRadius runes either side of
// the first hit, plus the hit's own offset inside that snippet. Both the
// search and the snippet bounds are computed over runes, not bytes, so a
// multi-byte character is never split and the offset lines up with how a
// phone's own string indexing counts characters.
func findSnippet(haystack, q string) (snippet string, offset int, ok bool) {
	if haystack == "" || q == "" {
		return "", 0, false
	}
	runes := []rune(haystack)
	idx := indexFold(runes, []rune(q))
	if idx < 0 {
		return "", 0, false
	}
	needleLen := len([]rune(q))
	start := max(0, idx-conversationSearchSnippetRadius)
	end := min(len(runes), idx+needleLen+conversationSearchSnippetRadius)
	return string(runes[start:end]), idx - start, true
}

// indexFold is strings.Index for rune slices, case-folded with
// unicode.ToLower on each rune -- good enough for the plain-text,
// human-typed queries this answers, without pulling in a full Unicode
// case-folding pass for a search box.
func indexFold(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	fold := func(rs []rune) []rune {
		out := make([]rune, len(rs))
		for i, r := range rs {
			out[i] = unicode.ToLower(r)
		}
		return out
	}
	h, n := fold(haystack), fold(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if h[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
