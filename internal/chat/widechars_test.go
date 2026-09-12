package chat

import (
	"strings"
	"testing"
)

// Characters that take two columns -- Chinese, Japanese, Korean, most emoji --
// are measured as two, so a line with them is wrapped to the pane rather than
// drawn wider than it and broken again by the terminal.
func TestWideCharactersAreMeasuredInColumns(t *testing.T) {
	if got := displayWidth("漢字 ok 🚀 é"); got != 4+1+2+1+2+1+1 {
		t.Errorf("displayWidth = %d", got)
	}

	var out strings.Builder
	p := newPrinter(&out, 20, false)
	p.text("漢字漢字 漢字漢字 漢字漢字\n")
	p.endMessage()
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if w := displayWidth(line); w > 20 {
			t.Errorf("drew a line %d columns wide in a pane of 20: %q", w, line)
		}
	}
	if !strings.Contains(out.String(), "漢字漢字 漢字漢字\n漢字漢字") {
		t.Errorf("the third word was not wrapped:\n%s", out.String())
	}

	for _, line := range wrapNotice("注意 これは とても 長い 通知 です から 折り返す 必要 が あります", 20) {
		if w := displayWidth(line); w > 20 {
			t.Errorf("wrapNotice made a line %d columns wide: %q", w, line)
		}
	}
	if w := displayWidth(clipTo("漢字漢字漢字漢字漢字漢字", 10)); w > 10 {
		t.Errorf("clipTo left %d columns of 10", w)
	}
}
