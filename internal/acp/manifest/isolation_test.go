package manifest

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/testiso"
)

const fakeEnv = "FLOCKDECK_MANIFEST_FAKE"

// TestMain keeps these tests off the real config directory, the stored API
// keys and the real network (see docs/testing.md). The handshake tests start
// a fake adapter, which is this test binary run again with fakeEnv set.
func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		runFake(mode)
		return
	}
	os.Exit(testiso.Main(m))
}

func runFake(mode string) {
	// These are descendants or never answer; they sleep without reading stdin,
	// which a descendant started with no stdin would find already closed.
	switch mode {
	case "silent":
		time.Sleep(time.Hour)
	case "brief":
		time.Sleep(20 * time.Second)
	case "descendant":
		time.Sleep(time.Hour)
	case "bigout":
		// What a program on PATH named node could print: a megabyte, no newline.
		os.Stdout.WriteString(strings.Repeat("v22.0.0 ", 1<<17))
		return
	}
	in := bufio.NewReader(os.Stdin)
	if _, err := in.ReadString('\n'); err != nil {
		os.Exit(0)
	}
	switch mode {
	case "ok":
		fmt.Println("starting up, this line is not JSON")
		fmt.Println(`{"jsonrpc":"2.0","method":"session/update","params":{}}`)
		fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"fake","version":"9.9.9"},"authMethods":[{"id":"api-key"},{"id":"oauth"}]}}`)
	case "error":
		fmt.Println(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`)
	case "v2":
		fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":2}}`)
	case "noversion":
		fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	case "exit":
		fmt.Fprintln(os.Stderr, "fatal: cannot start")
		os.Exit(3)
	case "exit-late":
		// Stdout ends first and the reason follows on stderr a moment later:
		// the order a loaded machine can give "exit", made certain.
		_ = os.Stdout.Close()
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "fatal: cannot start")
		os.Exit(3)
	case "holdstderr":
		// A descendant that keeps stdout and stderr open after this one is gone.
		exe, _ := os.Executable()
		c := exec.Command(exe)
		c.Env = append(os.Environ(), fakeEnv+"=descendant")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		_ = c.Start()
		fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`)
	case "exit-holdstderr":
		// Ends without answering, with a reason on stderr, while a descendant
		// keeps stderr open: the wait for the end of stderr has to give up.
		exe, _ := os.Executable()
		c := exec.Command(exe)
		c.Env = append(os.Environ(), fakeEnv+"=descendant")
		c.Stderr = os.Stderr
		_ = c.Start()
		fmt.Fprintln(os.Stderr, "fatal: cannot start")
		_ = os.Stdout.Close()
		os.Exit(3)
	case "escape":
		// A descendant that left the adapter's session and keeps the streams
		// open, so the tree can not reach it. It ends itself after 20 seconds.
		exe, _ := os.Executable()
		c := exec.Command(exe)
		c.Env = append(os.Environ(), fakeEnv+"=brief")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		leaveSession(c)
		_ = c.Start()
		fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`)
	}
	time.Sleep(50 * time.Millisecond)
}
