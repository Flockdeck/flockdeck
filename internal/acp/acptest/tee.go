package acptest

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// ArgTee makes a process a recording proxy for a real agent: the arguments
// after it are a log path, the agent's program and the program's arguments.
// Everything the client writes to the proxy goes to the program unchanged, and
// everything the program writes goes back unchanged. Each line is also written
// to the log with a prefix, "> " for the client's and "< " for the agent's, and a
// time. It is for a test that wants the raw traffic of a real agent, which the
// client does not keep. The log holds whatever the test sent and the agent
// answered, so the test must send nothing it would not put in a report. That includes the
// mcpServers of a session/new, which the tee does not redact as internal/record
// does.
const ArgTee = "--acptest-tee"

// runTee is the proxy. It returns the program's exit code.
func runTee(args []string) int {
	if len(args) < 2 {
		return 2
	}
	log, err := os.OpenFile(args[0], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 2
	}
	defer log.Close()
	var mu sync.Mutex
	note := func(prefix string, b []byte) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = log.WriteString(time.Now().UTC().Format("15:04:05.000") + " " + prefix + string(b))
	}
	cmd := exec.Command(args[1], args[2:]...)
	sysproc.NoWindow(cmd)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return 2
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 2
	}
	if err := cmd.Start(); err != nil {
		note("! ", []byte(err.Error()+"\n"))
		return 2
	}
	copyLines := func(dst io.Writer, src io.Reader, prefix string, closeDst bool) {
		r := bufio.NewReaderSize(src, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				note(prefix, line)
				_, _ = dst.Write(line)
			}
			if err != nil {
				break
			}
		}
		if c, ok := dst.(io.Closer); ok && closeDst {
			_ = c.Close()
		}
	}
	done := make(chan struct{})
	go func() {
		copyLines(os.Stdout, out, "< ", false)
		close(done)
	}()
	go copyLines(in, os.Stdin, "> ", true)
	<-done
	err = cmd.Wait()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}
