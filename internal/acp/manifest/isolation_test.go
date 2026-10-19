package manifest

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
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
	case "holdstderr":
		// A descendant that keeps stdout and stderr open after this one is gone.
		exe, _ := os.Executable()
		c := exec.Command(exe)
		c.Env = append(os.Environ(), fakeEnv+"=descendant")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		_ = c.Start()
		fmt.Println(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`)
	case "descendant":
		time.Sleep(time.Hour)
	case "silent":
		time.Sleep(time.Hour)
	}
	time.Sleep(50 * time.Millisecond)
}
