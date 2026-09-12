package chat

import "testing"

// A line that begins with a path is a question about it, not a command.
func TestALineBeginningWithAPathIsAPrompt(t *testing.T) {
	for line, want := range map[string]bool{
		"/model":                          true,
		"/model opus":                     true,
		"/history 10":                     true,
		"/nonsense":                       true,
		"/":                               true,
		"/usr/lib/libssl.so is missing":   false,
		"/c/Users/me/app.log says denied": false,
		"/etc/hosts":                      false,
		"/tmp/x.txt: no such file":        false,
		"hello":                           false,
	} {
		if got := isCommand(line); got != want {
			t.Errorf("isCommand(%q) = %v, want %v", line, got, want)
		}
	}

	wire := &scriptedWire{turns: []turnFunc{says("look at the path")}}
	run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "/usr/lib/libssl.so is missing\n/exit\n", wire)
	reqs := wire.requests()
	if len(reqs) != 1 || len(reqs[0].Messages) != 1 || reqs[0].Messages[0].Text != "/usr/lib/libssl.so is missing" {
		t.Errorf("the line was not sent as a prompt: %+v", reqs)
	}
}
