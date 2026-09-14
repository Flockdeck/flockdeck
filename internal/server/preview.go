package server

import (
	"regexp"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// This file is the desktop side of the phone's inbox row: a one-line preview
// of an agent pane's latest reply, sent as "last" on the pane's state (see
// control.go's paneView), so someone away from their desk can tell what an
// agent last said without opening its chat view. Documented in the
// scratchpad's convo-protocol.md.
//
// It is kept cheap: computed only when ConversationHookEvent says a pane's
// transcript may have grown -- the same signal conversation.go tails the
// chat view's own stream on, and at the same rate -- never rebuilt on a
// state push, which happens far more often than an agent actually replies.

// lastReplyView is a pane's latest assistant reply, trimmed to what an inbox
// row shows. Kind is always "reply" for now: room for the protocol to carry
// some other latest-event kind later without a breaking change.
type lastReplyView struct {
	Text string `json:"text"`
	TS   string `json:"ts"`
	Kind string `json:"kind"`
}

// lastReplyCap bounds how much of a reply the inbox row carries -- a row is a
// line or two, not the chat view.
const lastReplyCap = 140

// previewCache holds every agent pane's latest-reply preview, one entry
// each.
type previewCache struct {
	mu   sync.Mutex
	last map[string]lastReplyView
}

func newPreviewCache() previewCache { return previewCache{last: map[string]lastReplyView{}} }

// get returns paneID's preview, or ok=false when it has none yet -- no
// conversation adapter, or no reply seen so far -- which is why the field is
// left out of the pane's state.
func (c *previewCache) get(paneID string) (lastReplyView, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.last[paneID]
	return v, ok
}

func (c *previewCache) set(paneID string, v lastReplyView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last[paneID] = v
}

func (c *previewCache) drop(paneID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.last, paneID)
}

// notePreview updates paneID's preview from entries a refresh of its stream
// just produced, when one of them is a reply -- the newest, when several
// arrived in the same tick (an assistant turn with more than one text
// block).
func (s *Server) notePreview(paneID string, changed []transcript.Entry) {
	for i := len(changed) - 1; i >= 0; i-- {
		if changed[i].Kind == transcript.KindReply {
			s.preview.set(paneID, lastReplyView{
				Text: previewText(changed[i].Markdown),
				TS:   changed[i].TS,
				Kind: "reply",
			})
			return
		}
	}
}

// previewText turns a reply's markdown into the plain, single-line text an
// inbox row shows: common markdown syntax stripped, whitespace collapsed to
// single spaces, capped at lastReplyCap runes.
func previewText(md string) string {
	t := stripMarkdown(md)
	t = strings.Join(strings.Fields(t), " ")
	r := []rune(t)
	if len(r) <= lastReplyCap {
		return t
	}
	return string(r[:lastReplyCap]) + "…"
}

var (
	reCodeFence  = regexp.MustCompile("```[a-zA-Z0-9]*")
	reImage      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	reLink       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reHeading    = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	reListMarker = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+\.)\s+`)
	reBlockquote = regexp.MustCompile(`(?m)^\s*>\s?`)
	reEmphasis   = regexp.MustCompile("[*_~`]")
)

// stripMarkdown removes the markdown syntax common in an agent's reply --
// headings, emphasis, code fences and inline code, links and images (kept as
// their own label or alt text), list and blockquote markers -- without a full
// parser, which a one-line preview does not need.
func stripMarkdown(s string) string {
	s = reImage.ReplaceAllString(s, "$1")
	s = reLink.ReplaceAllString(s, "$1")
	s = reCodeFence.ReplaceAllString(s, "")
	s = reHeading.ReplaceAllString(s, "")
	s = reListMarker.ReplaceAllString(s, "")
	s = reBlockquote.ReplaceAllString(s, "")
	s = reEmphasis.ReplaceAllString(s, "")
	return s
}
