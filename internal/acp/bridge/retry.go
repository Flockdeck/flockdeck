package bridge

import (
	"fmt"
	"slices"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// retryWords are the words for the classes an adapter may name in an api_retry
// notice. A class that is not here is shown as "error"; what the agent wrote
// about the failure is never shown.
var retryWords = map[string]string{
	"overloaded":   "overloaded",
	"rate_limit":   "rate limited",
	"server_error": "server error",
	"network":      "network error",
	"other":        "error",
}

// maxRetryNumber bounds the attempt numbers a notice may carry.
const maxRetryNumber = 999

// retryRow is the one row a run of retries shows, and the state behind the
// activity line. A long loop of retries is one row that changes, not one row per retry.
type retryRow struct {
	it           *item
	attempt, max int
	class        string
}

func retryWord(class string) string {
	if w, ok := retryWords[class]; ok {
		return w
	}
	return retryWords["other"]
}

// retryNotice folds in an api_retry notice. A notice with numbers that make no
// sense is dropped. Call with b.mu held.
func (b *Bridge) retryNotice(u acp.Update) {
	a, m := u.NoticeAttempt, u.NoticeMax
	if a < 1 || m < 1 || a > m || m > maxRetryNumber {
		return
	}
	class := u.NoticeClass
	if _, ok := retryWords[class]; !ok {
		class = "other"
	}
	text := fmt.Sprintf("Claude is retrying (attempt %d of %d, %s)", a, m, retryWord(class))
	if r := b.retry; r != nil && slices.Contains(b.items, r.it) {
		r.attempt, r.max, r.class = a, m, class
		r.it.e.Text = text
		r.it.dirty = true
		b.reinline(r.it)
		return
	}
	b.closeMerges()
	b.retry = &retryRow{it: b.add(transcript.Entry{Kind: transcript.KindNotice, Text: text}), attempt: a, max: m, class: class}
}

// endRetry settles the retry row when the agent goes on or the turn ends: the
// row is kept, as a past note. Call with b.mu held.
func (b *Bridge) endRetry() {
	r := b.retry
	if r == nil {
		return
	}
	b.retry = nil
	if !slices.Contains(b.items, r.it) {
		return
	}
	n := "once"
	if r.attempt > 1 {
		n = fmt.Sprintf("%d times", r.attempt)
	}
	r.it.e.Text = fmt.Sprintf("Claude retried %s (%s)", n, retryWord(r.class))
	r.it.dirty, r.it.urgent = true, true
	b.reinline(r.it)
}

// SettleRetry ends a retry that is still showing, for a conversation that will
// not see another update or turn end (the agent was closed or restarted). The
// row becomes the past note.
func (b *Bridge) SettleRetry() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.endRetry()
}

// retryActivity is the activity line while a retry is going on, or "".
func (b *Bridge) retryActivity() string {
	if r := b.retry; r != nil {
		return fmt.Sprintf("retrying (attempt %d of %d, %s)", r.attempt, r.max, retryWord(r.class))
	}
	return ""
}
