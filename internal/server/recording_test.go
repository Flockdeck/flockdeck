package server

import (
	"strings"
	"testing"
)

func TestSpawnRecordIsRefusedUntilTheUserHasAgreedInTheWindow(t *testing.T) {
	if err := recordSpawnRefusal(false, true); err != nil {
		t.Errorf("an agent spawn was refused after the user agreed: %v", err)
	}
	err := recordSpawnRefusal(false, false)
	if err == nil || !strings.Contains(err.Error(), "turned on in the window") {
		t.Errorf("before the user agreed: %v", err)
	}
	if err := recordSpawnRefusal(true, true); err == nil || !strings.Contains(err.Error(), "shell") {
		t.Errorf("a shell was allowed to record: %v", err)
	}
}
