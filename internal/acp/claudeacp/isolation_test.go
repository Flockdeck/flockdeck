package claudeacp

import (
	"context"
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/testiso"
	"github.com/jmwri/flockdeck/internal/testiso/depth"
)

// TestMain keeps these tests off the real config directory and the network
// (see docs/testing.md). The test binary is also the fake claude and, for the
// interop test, the adapter: when it was started as one of them it runs as that
// and exits without the isolation. The chain is the test, the adapter, the fake
// claude: two ancestors at most, and the fake starts nothing.
func TestMain(m *testing.M) {
	depth.Guard()
	switch {
	case os.Getenv(envFake) == "1":
		runFake()
		return
	case os.Getenv(envAdapt) == "1":
		exe, _ := os.Executable()
		err := Run(context.Background(), os.Stdin, os.Stdout, Options{
			ClaudePath: exe,
			Env:        fakeEnvFromAdapter(),
			Stderr:     os.Stderr,
		})
		if err != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(testiso.Main(m))
}

// fakeEnvFromAdapter is the environment the adapter hands the fake claude: the
// adapter's own, with the adapter marker swapped for the fake's.
func fakeEnvFromAdapter() []string {
	var env []string
	for _, kv := range os.Environ() {
		if len(kv) >= len(envAdapt) && kv[:len(envAdapt)] == envAdapt {
			continue
		}
		env = append(env, kv)
	}
	return append(env, envFake+"=1")
}
