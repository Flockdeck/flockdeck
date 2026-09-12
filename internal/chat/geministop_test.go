package chat

import "testing"

// STOP is the one reason that means a Gemini answer is done; any other,
// including the ones added after this was written, is said.
func TestGeminiReportsEveryFinishReasonButStop(t *testing.T) {
	if err := geminiStopped("STOP", ""); err != nil {
		t.Errorf("STOP: %v", err)
	}
	for _, finish := range []string{"OTHER", "LANGUAGE", "UNEXPECTED_TOOL_CALL", "IMAGE_SAFETY", "SAFETY"} {
		if err := geminiStopped(finish, ""); err == nil {
			t.Errorf("%s read as a finished answer", finish)
		}
	}
}
