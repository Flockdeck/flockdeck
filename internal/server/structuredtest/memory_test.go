package structuredtest

import (
	"errors"
	"testing"
)

var errStale = errors.New("stale")

func TestMemoryConforms(t *testing.T) {
	Conformance(t, errStale, func(t *testing.T) Harness { return (&Memory{ErrStale: errStale}).Harness() })
}

// leaky takes an answer for any card with the request id, the mistake the
// conformance test is there to catch: it compares the agent's id and not the Seq.
type leaky struct{ Memory }

func (l *leaky) AnswerPermission(seq uint64, requestID, optionID string) error {
	return l.Memory.AnswerPermission(l.Memory.pendingSeq(), requestID, optionID)
}

func (m *Memory) pendingSeq() uint64 {
	if c := m.PendingPermission(); c != nil {
		return c.Seq
	}
	return 0
}

func TestTheConformanceTestCatchesAPaneThatIgnoresTheSeq(t *testing.T) {
	failures := Failures(errStale, func() Harness {
		l := &leaky{Memory{ErrStale: errStale}}
		h := l.Memory.Harness()
		h.Pane = l
		return h
	})
	if len(failures) == 0 {
		t.Error("a pane that answers whatever card is pending passed the conformance test")
	}
}

func TestAWellBehavedPaneHasNoFailures(t *testing.T) {
	if f := Failures(errStale, func() Harness { return (&Memory{ErrStale: errStale}).Harness() }); len(f) != 0 {
		t.Errorf("failures: %v", f)
	}
}
