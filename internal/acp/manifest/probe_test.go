package manifest

import (
	"strings"
	"testing"
)

func TestMinimalEnvKeepsOnlyPath(t *testing.T) {
	got := minimalEnvFrom([]string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-1", "HOME=/home/x", "OPENAI_API_KEY=k"})
	if s := strings.Join(got, " "); strings.Contains(s, "sk-1") || strings.Contains(s, "OPENAI") || !strings.Contains(s, "PATH=/bin") {
		t.Errorf("minimalEnvFrom = %v", got)
	}
}

func TestSystemProbeLooksUpPrograms(t *testing.T) {
	if _, err := SystemProbe().LookPath("flockdeck-no-such-program-7f3a"); err == nil {
		t.Error("a program that does not exist was found")
	}
}
