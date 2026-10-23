package workspace

import (
	"cmp"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// structuredDetailMax is the most of an entry's text, and of its diff, that
// Detail hands back. The bridge holds up to its own budget of detail in all;
// one answer is never more than this, so a second window asking for the same
// entry costs the same small copy and not another share of the budget.
const structuredDetailMax = 64 << 10

// structuredGone is what Detail answers for an entry the runtime no longer
// holds, or never had: a notice, so the window shows a line and not a spinner.
const structuredGone = "This entry is no longer held."

// StructuredStream is the conversation of one structured pane as a
// transcript.Stream, for the conversation hub. The entries are the runtime's:
// the bridge keeps them in memory up to its cap, and the pane keeps the history
// of earlier sessions up to maxPaneHistory, so nothing here holds more than
// those two together. The stream reads the pane's current runtime on every call,
// so a restart is seen as a new runtime and not as a stale one.
//
// Every slice it returns is its own copy, made under its lock: the bridge
// appends while a window is paged, and a page is never cut from a slice the
// bridge can still change.
type StructuredStream struct {
	src func() (view structuredView, ok bool)

	mu     sync.Mutex
	tok    any // which runtime cur and last were read from
	detail func(string) (transcript.Detail, bool)
	cur    []transcript.Entry
	last   map[string]transcript.Entry // what Refresh last handed out, by id
	reset  bool
}

// StructuredStream is the stream of the structured pane id, or false for a pane
// that is not one.
func (w *Workspace) StructuredStream(id string) (*StructuredStream, bool) {
	if _, ok := w.structuredRuntimeOf(id); !ok {
		return nil, false
	}
	return newStructuredStream(func() (structuredView, bool) {
		sr, ok := w.structuredRuntimeOf(id)
		if !ok {
			return structuredView{}, false
		}
		return structuredView{token: sr, entries: sr.Entries(), detail: sr.rt.Detail}, true
	}), true
}

// structuredView is one reading of a pane's conversation: which runtime it came
// from, the entries (a copy), and how to fetch an entry's detail from it.
type structuredView struct {
	token   any
	entries []transcript.Entry
	detail  func(string) (transcript.Detail, bool)
}

func newStructuredStream(src func() (structuredView, bool)) *StructuredStream {
	return &StructuredStream{src: src, last: map[string]transcript.Entry{}}
}

func (w *Workspace) structuredRuntimeOf(id string) (*structuredRuntime, bool) {
	p := w.Pane(id)
	if p == nil {
		return nil, false
	}
	sr, ok := p.setRuntime().(*structuredRuntime)
	return sr, ok
}

// load reads the pane's runtime and entries into cur, and notes a restart. It
// returns false when the pane is gone or no longer structured, leaving cur as it
// was. s.mu must be held.
func (s *StructuredStream) load() bool {
	v, ok := s.src()
	if !ok {
		return false
	}
	if s.tok != nil && s.tok != v.token {
		// A restart: the earlier session's entries are now history under new ids,
		// so nothing the window holds matches. It is told to start again.
		s.reset = true
		s.last = map[string]transcript.Entry{}
	}
	s.tok, s.detail = v.token, v.detail
	s.cur = v.entries
	return true
}

// Refresh returns the entries new or changed since the last call, oldest first.
func (s *StructuredStream) Refresh() []transcript.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.load() {
		return nil
	}
	var changed []transcript.Entry
	next := make(map[string]transcript.Entry, len(s.cur))
	for _, e := range s.cur {
		next[e.ID] = e
		if old, seen := s.last[e.ID]; !seen || !reflect.DeepEqual(old, e) {
			changed = append(changed, e)
		}
	}
	s.last = next
	return changed
}

// Snapshot returns every entry now, oldest first. The slice is the caller's to
// read; the stream does not change it.
func (s *StructuredStream) Snapshot() []transcript.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return append([]transcript.Entry(nil), s.cur...)
}

// Detail is the full text of an entry. An entry of an earlier session has only
// what it carried inline: the runtime holds the detail of the current session
// alone. An id nobody holds is answered with a notice.
func (s *StructuredStream) Detail(id string) (transcript.Detail, bool) {
	s.mu.Lock()
	s.load()
	fetch := s.detail
	var inline *transcript.Entry
	for i := range s.cur {
		if s.cur[i].ID == id {
			e := s.cur[i]
			inline = &e
			break
		}
	}
	s.mu.Unlock()
	if inline == nil {
		return transcript.Detail{Text: structuredGone}, true
	}
	// A prompt or notice carries its words in Text, a reply in Markdown.
	text, diff := cmp.Or(inline.Text, inline.Markdown), inline.Diff
	if fetch != nil {
		if d, ok := fetch(id); ok {
			if d.Text != "" {
				text = d.Text
			}
			if d.Diff != "" {
				diff = d.Diff
			}
		}
	}
	return transcript.Detail{Text: cutUTF8(text, structuredDetailMax), Diff: cutUTF8(diff, structuredDetailMax)}, true
}

// Reset says once that the pane was restarted since it was last asked.
func (s *StructuredStream) Reset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	r := s.reset
	s.reset = false
	return r
}

// SetLight does nothing: the entries are held by the runtime whether or not a
// window has the pane open, and the stream keeps no copy of its own beyond the
// last Snapshot.
func (s *StructuredStream) SetLight(bool) {}

// EntryCount is how many entries the pane holds now.
func (s *StructuredStream) EntryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return len(s.cur)
}

// cutUTF8 cuts s to at most max bytes, between characters.
func cutUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.ToValidUTF8(s[:n], "")
}

var _ transcript.Stream = (*StructuredStream)(nil)
