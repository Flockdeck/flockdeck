package codexacp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake codex is this test binary run again. It answers the requests of a
// session by itself (initialize, thread/start, model/list and so on) and plays
// a script besides: a list of steps that wait for a line on stdin, write a line
// to stdout, sleep, check that something does not arrive, or exit. Everything
// that arrives on stdin, the arguments, the environment and the process id are
// written to a log file for the test to read.

const (
	envFake   = "FD_CODEXACP_FAKE"
	envAdapt  = "FD_CODEXACP_ADAPTER" // the test binary runs as the adapter
	envScript = "FD_CODEXACP_SCRIPT"
	envLog    = "FD_CODEXACP_LOG"
	envNoTurn = "FD_CODEXACP_TURNDELAY" // milliseconds before the turn/start response
	envErr    = "FD_CODEXACP_ERR"       // a method that is answered with an error
	envSlow   = "FD_CODEXACP_SLOWSTART" // milliseconds before the thread/start response
	envStderr = "FD_CODEXACP_STDERR"    // a line written to stderr at start
)

type step struct {
	Expect string          `json:"expect,omitempty"` // wait for a stdin line containing this
	Emit   json.RawMessage `json:"emit,omitempty"`   // write this line to stdout
	Sleep  int             `json:"sleep,omitempty"`  // milliseconds
	Absent string          `json:"absent,omitempty"` // for Ms, no stdin line may contain this
	Ms     int             `json:"ms,omitempty"`
	Exit   *int            `json:"exit,omitempty"`
	Big    int             `json:"big,omitempty"` // write a line of this many bytes
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
func big(n int) step               { return step{Big: n} }

// note builds a notification line.
func note(method string, params string) step {
	return emit(`{"method":"` + method + `","params":` + params + `}`)
}

func answer(method string, params map[string]any) any {
	switch method {
	case "initialize":
		return map[string]any{"userAgent": "fake-codex/0.0.0"}
	case "thread/start":
		return map[string]any{"thread": map[string]any{"id": "thr-1"}, "model": "gpt-fake", "cwd": "x"}
	case "thread/resume":
		return map[string]any{"thread": map[string]any{"id": params["threadId"]}, "model": "gpt-fake"}
	case "model/list":
		return map[string]any{"data": []any{
			map[string]any{"id": "gpt-fake", "model": "gpt-fake", "displayName": "GPT Fake", "description": "the default"},
			map[string]any{"id": "gpt-small", "model": "gpt-small", "displayName": "Small"},
		}}
	case "skills/list":
		return map[string]any{"data": []any{map[string]any{"cwd": "x", "skills": []any{
			map[string]any{"name": "deploy", "description": "Deploy it", "path": "/s/deploy/SKILL.md", "enabled": true},
			map[string]any{"name": "off", "description": "disabled", "path": "/s/off/SKILL.md", "enabled": false},
		}}}}
	case "turn/start", "review/start":
		return map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}}
	}
	return map[string]any{}
}

func runFake() {
	var out sync.Mutex
	w := bufio.NewWriterSize(os.Stdout, 1<<16)
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
	logf("PID: %d", os.Getpid())
	for _, kv := range os.Environ() {
		logf("ENV: %s", kv)
	}
	if s := os.Getenv(envStderr); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}

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
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params map[string]any  `json:"params"`
				}
				if json.Unmarshal([]byte(s), &m) == nil && m.Method != "" && len(m.ID) > 0 {
					resp := map[string]any{"id": m.ID}
					if ms, _ := strconv.Atoi(os.Getenv(envSlow)); ms > 0 && m.Method == "thread/start" {
						time.Sleep(time.Duration(ms) * time.Millisecond)
					}
					switch {
					case m.Method == os.Getenv(envErr):
						resp["error"] = map[string]any{"code": -32000, "message": "SECRET-codex-detail in " + m.Method}
					case m.Method == "turn/start" && os.Getenv(envNoTurn) != "":
						// The response comes late, after the notifications.
						ms, _ := strconv.Atoi(os.Getenv(envNoTurn))
						resp["result"] = answer(m.Method, m.Params)
						b, _ := json.Marshal(resp)
						go func() { time.Sleep(time.Duration(ms) * time.Millisecond); write(string(b)) }()
						resp = nil
					default:
						resp["result"] = answer(m.Method, m.Params)
					}
					if resp != nil {
						b, _ := json.Marshal(resp)
						write(string(b))
					}
				}
				lines <- s
			}
			if err != nil {
				logf("STDIN_CLOSED")
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
		case st.Big > 0:
			write(`{"method":"x","params":{"pad":"` + strings.Repeat("a", st.Big) + `"}}`)
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
	// Script done: keep answering requests until stdin closes.
	for range lines {
	}
}

// fakeEnv is the environment the fake codex is started with.
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

func logHas(log []string, sub string) bool { return logFind(log, sub) != "" }

// logFind returns the first stdin line (without its IN: prefix) that contains sub.
func logFind(log []string, sub string) string {
	for _, l := range log {
		if strings.HasPrefix(l, "IN: ") && strings.Contains(l, sub) {
			return strings.TrimPrefix(l, "IN: ")
		}
	}
	return ""
}
