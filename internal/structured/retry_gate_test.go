package structured

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/agent"
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
