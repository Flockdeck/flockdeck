package structured

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/shellrun"
)

func retryNotice() acp.Update {
	return acp.Update{Kind: acp.UpdateNotice, NoticeCode: acp.NoticeAPIRetry, NoticeAttempt: 2, NoticeMax: 10, NoticeClass: "overloaded"}
}

// Only Flockdeck's own Claude adapter can put a retry row and activity line up.
func TestRetryNoticeFromAnotherAgentIsIgnored(t *testing.T) {
	r := runtimeWith(agent.Spec{ID: "fake"})
	r.br.StartTurn("go")
	h := handler{Handler: updateOnly{r.br}, r: r}
	h.Update(retryNotice())
	if got := r.br.Activity(); got != "" {
		t.Errorf("activity = %q", got)
	}
	for _, e := range r.br.Snapshot() {
		if e.Kind == "notice" {
			t.Errorf("another agent made a notice: %q", e.Text)
		}
	}
}

func TestRetryNoticeFromTheOwnClaudeAdapterIsTaken(t *testing.T) {
	r := runtimeWith(claudeSpec(t))
	r.br.StartTurn("go")
	h := handler{Handler: updateOnly{r.br}, r: r}
	h.Update(retryNotice())
	if got := r.br.Activity(); !strings.Contains(got, "attempt 2 of 10, overloaded") {
		t.Errorf("activity = %q", got)
	}
}

// A retry the agent was in the middle of when the pane closes or restarts is
// settled: the loop does not reach TurnEnd, so nothing else would.
func TestClosingSettlesARetryRow(t *testing.T) {
	r := runtimeWith(claudeSpec(t))
	r.br.StartTurn("go")
	h := handler{Handler: updateOnly{r.br}, r: r}
	h.Update(retryNotice())
	r.SettleRetry()
	for _, e := range r.br.Snapshot() {
		if e.Kind == "notice" && e.Text != "Claude retried 2 times (overloaded)" {
			t.Errorf("notice = %q", e.Text)
		}
	}
	r2 := runtimeWith(claudeSpec(t))
	r2.br.StartTurn("go")
	handler{Handler: updateOnly{r2.br}, r: r2}.Update(retryNotice())
	r2.cancel = func() {}
	r2.shell = shellrun.New(t.TempDir(), nil, func() {})
	r2.finish(PhaseClosed)
	for _, e := range r2.br.Snapshot() {
		if e.Kind == "notice" && strings.Contains(e.Text, "is retrying") {
			t.Errorf("closed pane still says %q", e.Text)
		}
	}
}
