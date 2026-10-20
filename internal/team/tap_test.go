package team

import (
	"strings"
	"testing"
	"time"
)

func TestTapLogKeepsTheLastTapsAndNoText(t *testing.T) {
	var l TapLog
	for i := 0; i < MaxTaps+5; i++ {
		l.Add(Tap{Time: time.Unix(int64(i), 0), From: "a", To: "b", Tool: "SendMessage", Understood: true})
	}
	got := l.Taps()
	if len(got) != MaxTaps || got[0].Time.Unix() != 5 {
		t.Fatalf("kept %d taps, first at %v", len(got), got[0].Time)
	}
	if l.Rev() != MaxTaps+5 {
		t.Errorf("rev = %d", l.Rev())
	}
}

func TestTapLogCleansToolAndDropsTheRecipientOfAnUnderstoodTap(t *testing.T) {
	var l TapLog
	l.Add(Tap{From: "a", To: "b", Tool: "Send\x1bMessage" + strings.Repeat("x", 200), Understood: false})
	got := l.Taps()[0]
	if got.To != "" {
		t.Errorf("a call not understood has a recipient %q", got.To)
	}
	if len(got.Tool) != MaxTapTool || strings.ContainsRune(got.Tool, 0x1b) {
		t.Errorf("tool = %q", got.Tool)
	}
}
