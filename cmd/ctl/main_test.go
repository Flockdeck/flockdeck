package main

import "testing"

func TestFocusedPaneFollowsTheActiveTab(t *testing.T) {
	data := []byte(`{"type":"state","activeTab":"t2","tabs":[{"id":"t1","focus":"p1"},{"id":"t2","focus":"p9"}]}`)
	got, err := focusedPane(data)
	if err != nil || got != "p9" {
		t.Errorf("focusedPane = %q, %v; want p9", got, err)
	}
	if got, _ := focusedPane([]byte(`{"activeTab":"gone","tabs":[]}`)); got != "" {
		t.Errorf("focusedPane with no matching tab = %q, want empty", got)
	}
	if _, err := focusedPane([]byte(`nope`)); err == nil {
		t.Error("focusedPane accepted invalid JSON")
	}
}
