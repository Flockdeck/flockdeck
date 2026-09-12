package tool

import (
	"strings"
	"testing"
)

// A command can print far more than the model will be shown, and what is kept
// of it must stay the size of what is shown however much is printed.
func TestCaptureKeepsBothEndsInBoundedMemory(t *testing.T) {
	var c capture
	c.Write([]byte("BEGIN\n"))
	chunk := []byte(strings.Repeat("m", 4096))
	for i := 0; i < 5000; i++ { // twenty megabytes
		c.Write(chunk)
		if len(c.head)+len(c.tail) > 2*commandMaxOutput {
			t.Fatalf("holding %d bytes after %d writes", len(c.head)+len(c.tail), i+1)
		}
	}
	c.Write([]byte("\nEND"))
	got := c.String()
	if !strings.HasPrefix(got, "BEGIN") || !strings.HasSuffix(got, "END") {
		t.Error("the beginning and the end must both survive")
	}
	if !strings.Contains(got, "of output omitted") || len(got) > commandMaxOutput+200 {
		t.Errorf("returned %d bytes, want the bound and a note of the gap", len(got))
	}
}

func TestRunCommandFloodIsClippedWhileItRuns(t *testing.T) {
	tl := &runCommand{root: newRoot(t)}
	got, err := call(t, tl, map[string]any{"command": helperLine(t, "flood")})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"BEGIN", "END", "of output omitted", "[exit status 0]"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if len(got) > commandMaxOutput+200 {
		t.Errorf("returned %d bytes", len(got))
	}
}
