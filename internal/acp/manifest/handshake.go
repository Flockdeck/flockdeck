package manifest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/proctree"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// HandshakeConfig says how to start an adapter for a handshake. Nothing is
// taken from the environment: Env is exactly what the adapter gets.
type HandshakeConfig struct {
	// Path is the program, an absolute path.
	Path string
	Args []string
	// Dir is the working directory, an absolute path.
	Dir string
	Env []string
	// Timeout is how long the whole handshake may take. Default 60 seconds.
	Timeout time.Duration
}

// HandshakeResult is what the adapter said to initialize.
type HandshakeResult struct {
	ProtocolVersion int
	AgentName       string
	AgentVersion    string
	AuthMethods     []string
	Took            time.Duration
}

const (
	maxHandshakeLine = 4 << 20
	stderrTail       = 2 << 10
	handshakeGrace   = 3 * time.Second
)

// Handshake starts the adapter, sends initialize with protocol version 1 and
// no client capabilities, and reads the answer. It does not log in, send a
// prompt or open a session, and it ends the adapter before it returns. It is
// the canary's check that an adapter still speaks the protocol this client
// does.
func Handshake(ctx context.Context, cfg HandshakeConfig) (HandshakeResult, error) {
	var res HandshakeResult
	if !filepath.IsAbs(cfg.Path) || !filepath.IsAbs(cfg.Dir) {
		return res, errors.New("handshake: path and dir must be absolute")
	}
	if cfg.Env == nil {
		return res, errors.New("handshake: env is required; pass the environment the adapter is to have")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	cmd := exec.Command(cfg.Path, cfg.Args...)
	cmd.Dir, cmd.Env = cfg.Dir, cfg.Env
	sysproc.NoWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return res, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return res, err
	}
	started := time.Now()
	tree, err := proctree.Start(cmd)
	if err != nil {
		return res, fmt.Errorf("handshake: start: %w", err)
	}
	// The process is reaped here, not by cmd.Wait, which would wait for stdout
	// and stderr to reach EOF: a descendant that holds them would hang it.
	proc := cmd.Process
	reaped := make(chan struct{})
	go func() { _, _ = proc.Wait(); close(reaped) }()

	var tail tailBuffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = io.Copy(&tail, stderr) }()
	defer func() {
		_ = stdin.Close()
		tree.End(proc, reaped, handshakeGrace)
		// Closing the read ends frees the readers even if a descendant that
		// got away still holds the other ends.
		_ = stdout.Close()
		_ = stderr.Close()
		wg.Wait()
	}()

	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{},"clientInfo":{"name":"flockdeck-canary","version":"0"}}}` + "\n"
	if _, err := io.WriteString(stdin, req); err != nil {
		return res, fmt.Errorf("handshake: write: %w%s", err, tail.suffix())
	}

	type reply struct {
		res HandshakeResult
		err error
	}
	got := make(chan reply, 1)
	go func() {
		br := bufio.NewReaderSize(stdout, 64<<10)
		for {
			line, err := readLine(br, maxHandshakeLine)
			if len(line) > 0 {
				if r, ok, perr := parseInitialize(line); ok {
					got <- reply{r, perr}
					return
				}
			}
			if err != nil {
				got <- reply{err: fmt.Errorf("no answer to initialize: %w", err)}
				return
			}
		}
	}()
	select {
	case r := <-got:
		r.res.Took = time.Since(started)
		if r.err != nil {
			return r.res, fmt.Errorf("handshake: %w%s", r.err, tail.suffix())
		}
		return r.res, nil
	case <-ctx.Done():
		return res, fmt.Errorf("handshake: no answer within %v%s", cfg.Timeout, tail.suffix())
	}
}

// readLine reads one line, dropping what is past max.
func readLine(br *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(out) < max {
			out = append(out, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSpace(out), err
	}
}

// parseInitialize says whether line is the response to request 1 and, if so,
// what it holds. Lines that are not JSON, and other messages, are skipped.
func parseInitialize(line []byte) (HandshakeResult, bool, error) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result *struct {
			ProtocolVersion *int `json:"protocolVersion"`
			AgentInfo       struct {
				Name, Version string
			} `json:"agentInfo"`
			AuthMethods []struct {
				ID string `json:"id"`
			} `json:"authMethods"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(line, &m) != nil || m.Method != "" || string(m.ID) != "1" {
		return HandshakeResult{}, false, nil
	}
	if m.Error != nil {
		return HandshakeResult{}, true, fmt.Errorf("initialize failed: code %d: %s", m.Error.Code, clipText(m.Error.Message, 200))
	}
	if m.Result == nil || m.Result.ProtocolVersion == nil {
		return HandshakeResult{}, true, errors.New("initialize answered with no protocolVersion")
	}
	r := HandshakeResult{ProtocolVersion: *m.Result.ProtocolVersion, AgentName: clipText(m.Result.AgentInfo.Name, 100), AgentVersion: clipText(m.Result.AgentInfo.Version, 100)}
	for _, a := range m.Result.AuthMethods {
		r.AuthMethods = append(r.AuthMethods, clipText(a.ID, 100))
	}
	if r.ProtocolVersion != 1 {
		return r, true, fmt.Errorf("the adapter speaks protocol version %d, this client speaks 1", r.ProtocolVersion)
	}
	return r, true, nil
}

// tailBuffer keeps the last stderrTail bytes written to it.
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > stderrTail {
		t.b = append([]byte(nil), t.b[len(t.b)-stderrTail:]...)
	}
	return len(p), nil
}

// suffix is the adapter's last words, for an error message.
func (t *tailBuffer) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(string(t.b))
	if s == "" {
		return ""
	}
	return "; stderr ended: " + s
}
