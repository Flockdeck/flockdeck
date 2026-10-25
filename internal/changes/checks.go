package changes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jmwri/flockdeck/internal/proctree"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// Limits of the check definitions and of one run of a check.
const (
	MaxChecks       = 20
	MaxCheckName    = 60
	MaxCheckCmd     = 1000
	MaxSummary      = 200
	CheckTimeout    = 10 * time.Minute
	CheckOutputCap  = 64 << 10
	checkKillGrace  = 2 * time.Second
	checkStateLimit = 1 << 20
)

// Check is one result of a check, in the shape of the schema's check.
type Check struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Source     string `json:"source"`
	AtSha      string `json:"atSha,omitempty"`
	// Worktree is set when the run saw files that are not in the head commit,
	// the same marker a diff carries. Such a pass says nothing about the head,
	// and the merge gate must never count it.
	Worktree bool `json:"worktree,omitempty"`
}

// CheckDef is one command the person put in their check file.
type CheckDef struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

// LoadChecks reads the check definitions at path. The file is Flockdeck's own,
// in the config directory, and edited by hand by the person: no command comes
// from a window, a repository or an agent. A missing file is reported as
// os.ErrNotExist. Entries over the limits, repeated names and entries with no
// name or command are dropped.
func LoadChecks(path string) ([]CheckDef, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var doc struct {
		Checks []CheckDef `json:"checks"`
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(io.LimitReader(f, checkStateLimit+1)); err != nil {
		return nil, err
	}
	if buf.Len() > checkStateLimit {
		return nil, errors.New("the check file is too large")
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		return nil, errors.New("the check file is not valid JSON")
	}
	seen := map[string]bool{}
	var out []CheckDef
	for _, d := range doc.Checks {
		name := cleanLine(d.Name, MaxCheckName)
		cmd := strings.TrimSpace(d.Cmd)
		if name == "" || cmd == "" || len(cmd) > MaxCheckCmd || strings.ContainsRune(cmd, 0) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, CheckDef{Name: name, Cmd: cmd})
		if len(out) == MaxChecks {
			break
		}
	}
	return out, nil
}

// cleanLine is one line of at most max runes with no control or format
// character in it.
func cleanLine(s string, max int) string {
	var b strings.Builder
	n := 0
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		if n >= max {
			break
		}
		if space {
			if n+1 >= max {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// Summary is the first line of a check's own output with something in it,
// cleaned. It is the only text of the output that is kept.
func Summary(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if s := cleanLine(line, MaxSummary); s != "" {
			return s
		}
	}
	return ""
}

// checkEnv is the environment a check runs in: the few variables a build needs
// to find its tools and a place to write, and nothing else. It holds no token
// or key of Flockdeck's or of the person's session.
func checkEnv() []string {
	names := []string{"PATH", "LANG", "LC_ALL", "TZ"}
	if runtime.GOOS == "windows" {
		names = append(names, "SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "TEMP", "TMP",
			"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)",
			"ProgramData", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS")
	} else {
		names = append(names, "HOME", "TMPDIR", "USER", "SHELL")
	}
	var env []string
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			env = append(env, n+"="+v)
		}
	}
	return env
}

// cappedWriter keeps the first max bytes written to it and counts on.
type cappedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		w.buf.Write(p[:room])
	}
	return len(p), nil
}

func (w *cappedWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

// RunCheck runs one check in dir and returns its result. dir is the agent's
// checkout: the command runs there with the person's rights, exactly as if the
// person had run it, with a clean environment, at most timeout and its output
// capped. When the time is up, or ctx ends, the command and everything it
// started are killed. A tree with files that are not in the head commit is
// run on as it is and marked Worktree.
func RunCheck(ctx context.Context, dir string, def CheckDef, headSha string, dirty bool, timeout time.Duration) Check {
	res := Check{Name: def.Name, Source: "local", AtSha: headSha, Worktree: dirty,
		StartedAt: time.Now().UTC().Format(time.RFC3339)}
	finish := func(state, summary string) Check {
		res.State = state
		res.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if dirty {
			note := "run on uncommitted files, not a pass for the head commit"
			if summary != "" {
				summary = cleanLine(summary, MaxSummary-len(note)-3)
				summary += " (" + note + ")"
			} else {
				summary = note
			}
			res.Summary = cleanLine(summary, MaxSummary)
		} else {
			res.Summary = summary
		}
		return res
	}

	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shellCommand(rctx, def.Cmd)
	cmd.Dir = dir
	cmd.Env = checkEnv()
	cmd.Stdin = nil
	out := &cappedWriter{max: CheckOutputCap}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = time.Second
	sysproc.NoWindow(cmd)
	tree, err := proctree.Start(cmd)
	if err != nil {
		return finish("fail", "the command could not be started")
	}
	done := make(chan error, 1)
	reaped := make(chan struct{})
	go func() {
		err := cmd.Wait()
		close(reaped)
		done <- err
	}()
	var waitErr error
	timedOut := false
	select {
	case waitErr = <-done:
	case <-rctx.Done():
		timedOut = true
		tree.End(cmd.Process, nil, checkKillGrace)
		waitErr = <-done
	}
	tree.End(cmd.Process, reaped, checkKillGrace)
	summary := Summary(out.bytes())
	switch {
	case timedOut && ctx.Err() == nil:
		return finish("fail", "timed out after "+timeout.String())
	case timedOut:
		return finish("fail", "stopped")
	case waitErr != nil:
		if summary == "" {
			summary = "the command failed"
		}
		return finish("fail", summary)
	}
	return finish("pass", summary)
}
