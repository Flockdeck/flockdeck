package claudeacp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake claude is this test binary run again. It replays a script: a list of
// steps that wait for a line on stdin, write a line to stdout, sleep, check
// that something does not arrive, or exit. Control requests that are not part
// of the script (initialize, set_model, interrupt and so on) are answered at
// once, as claude answers them. Everything that arrives on stdin, and the
// arguments, are written to a log file for the test to read.

const (
	envFake   = "FD_CLAUDEACP_FAKE"
	envScript = "FD_CLAUDEACP_SCRIPT"
	envLog    = "FD_CLAUDEACP_LOG"
	envAdapt  = "FD_CLAUDEACP_ADAPTER"
)

type step struct {
	Expect string          `json:"expect,omitempty"` // wait for a stdin line containing this
	Emit   json.RawMessage `json:"emit,omitempty"`   // write this line to stdout
	Sleep  int             `json:"sleep,omitempty"`  // milliseconds
	Absent string          `json:"absent,omitempty"` // for Ms, no stdin line may contain this
	Ms     int             `json:"ms,omitempty"`
	Exit   *int            `json:"exit,omitempty"`
}

func expect(s string) step { return step{Expect: s} }
func emit(line string) step {
	if !json.Valid([]byte(line)) {
		panic("not JSON: " + line)
	}
	return step{Emit: json.RawMessage(line)}
}
func sleep(ms int) step            { return step{Sleep: ms} }
func absent(s string, ms int) step { return step{Absent: s, Ms: ms} }
func exitWith(code int) step       { return step{Exit: &code} }

// canned answers to control requests claude is asked outside the script.
func controlAnswer(subtype string, req map[string]any) any {
	switch subtype {
	case "initialize":
		return map[string]any{
			"commands": []any{
				map[string]any{"name": "compact", "description": "Clear history but keep a summary", "argumentHint": "<optional custom summarization instructions>", "builtin": true},
				map[string]any{"name": "/review", "description": "Review a pull request", "argumentHint": "", "builtin": true},
			},
			"models": []any{
				map[string]any{"value": "default", "displayName": "Default (recommended)", "description": "Opus"},
				map[string]any{"value": "haiku", "displayName": "Haiku", "description": "Fastest"},
			},
			"permissionMode": "default",
		}
	case "set_permission_mode":
		return map[string]any{"mode": req["mode"]}
	case "set_model":
		return map[string]any{"model": "claude-haiku-5-5"}
	}
	return map[string]any{}
}

func runFake() {
	var out sync.Mutex
	w := bufio.NewWriter(os.Stdout)
	write := func(line string) {
		out.Lock()
		w.WriteString(line + "\n")
		w.Flush()
		out.Unlock()
	}
	var logMu sync.Mutex
	logf := func(format string, a ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		f, err := os.OpenFile(os.Getenv(envLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		fmt.Fprintf(f, format+"\n", a...)
		f.Close()
	}
	logf("ARGS: %s", strings.Join(os.Args[1:], " "))

	var steps []step
	if b, err := os.ReadFile(os.Getenv(envScript)); err == nil {
		_ = json.Unmarshal(b, &steps)
	}

	lines := make(chan string, 1024)
	go func() {
		br := bufio.NewReaderSize(os.Stdin, 1<<20)
		for {
			l, err := br.ReadString('\n')
			if s := strings.TrimSpace(l); s != "" {
				logf("IN: %s", s)
				var m struct {
					Type      string         `json:"type"`
					RequestID string         `json:"request_id"`
					Request   map[string]any `json:"request"`
				}
				if json.Unmarshal([]byte(s), &m) == nil && m.Type == "control_request" {
					sub, _ := m.Request["subtype"].(string)
					b, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{
						"subtype": "success", "request_id": m.RequestID, "response": controlAnswer(sub, m.Request)}})
					write(string(b))
				}
				lines <- s
			}
			if err != nil {
				close(lines)
				return
			}
		}
	}()

	for _, st := range steps {
		switch {
		case st.Expect != "":
			found := false
			for l := range lines {
				if strings.Contains(l, st.Expect) {
					found = true
					break
				}
			}
			if !found {
				logf("MISSING: %s", st.Expect)
				os.Exit(8)
			}
		case st.Emit != nil:
			write(string(st.Emit))
		case st.Sleep > 0:
			time.Sleep(time.Duration(st.Sleep) * time.Millisecond)
		case st.Absent != "":
			deadline := time.After(time.Duration(st.Ms) * time.Millisecond)
		wait:
			for {
				select {
				case l, ok := <-lines:
					if !ok {
						break wait
					}
					if strings.Contains(l, st.Absent) {
						logf("VIOLATION: %s arrived early", st.Absent)
					}
				case <-deadline:
					break wait
				}
			}
		case st.Exit != nil:
			os.Exit(*st.Exit)
		}
	}
	// Script done: keep answering control requests until stdin closes.
	for range lines {
	}
}

// fakeEnv is the environment the fake claude is started with. It is not the
// adapter's own, so the fake does not take itself for an adapter.
func fakeEnv(t testing.TB, steps []step) (env []string, logPath string) {
	t.Helper()
	dir := t.TempDir()
	script := dir + string(os.PathSeparator) + "script.json"
	logPath = dir + string(os.PathSeparator) + "log.txt"
	b, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"PATH", "SYSTEMROOT", "SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env, envFake+"=1", envScript+"="+script, envLog+"="+logPath), logPath
}

func readLog(path string) []string {
	b, _ := os.ReadFile(path)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
