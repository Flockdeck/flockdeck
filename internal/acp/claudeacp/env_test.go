package claudeacp

import "testing"

// A nil Env on an exec.Cmd means the parent's environment, so dropping every
// variable must leave an empty list, not nil.
func TestCleanEnvIsNeverNil(t *testing.T) {
	got := cleanEnv([]string{"FLOCKDECK_A=1", "perch_b=2"})
	if got == nil || len(got) != 0 {
		t.Errorf("cleanEnv = %#v", got)
	}
	if got := cleanEnv([]string{"FLOCKDECK_A=1", "KEEP=2"}); len(got) != 1 || got[0] != "KEEP=2" {
		t.Errorf("cleanEnv = %#v", got)
	}
}
