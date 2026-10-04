package helpers

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The fake helper is this test binary run again as a child, in the pattern the
// repository's other tests use for a program they cannot ship. The supervisor
// starts it as it would start lens: from the installed folder, with an
// argument list from the entry. It prints the banner, serves /healthz and
// /readyz, and acts out whatever its --mode says.

// TestFakeHelperProcess is the child. Run normally it returns at once.
func TestFakeHelperProcess(t *testing.T) {
	args := fakeArgs()
	if args == nil {
		return
	}
	runFakeHelper(args)
	os.Exit(0)
}

// TestCtrlBreakSender is the process that sends CTRL_BREAK on Windows, in the
// place of the flockdeck subcommand. Run normally it returns at once.
func TestCtrlBreakSender(t *testing.T) {
	args := fakeArgs()
	if len(args) != 1 {
		return
	}
	pid, err := strconv.Atoi(args[0])
	if err != nil {
		os.Exit(2)
	}
	if err := CtrlBreak(pid); err != nil {
		fmt.Fprintln(os.Stderr, "ctrl-break:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// fakeArgs is what follows "--" on the command line, or nil when there is no
// "--" (a normal test run).
func fakeArgs() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

func runFakeHelper(args []string) {
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprintln(os.Stderr, "fake helper: expected serve")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	host := fs.String("host", "127.0.0.1", "")
	port := fs.Int("port", 0, "")
	mode := fs.String("mode", "normal", "")
	marker := fs.String("marker", "", "file written when the helper is asked to stop")
	state := fs.String("state", "", "file each run appends its port to")
	after := fs.Duration("after", 0, "when a mode needs a delay")
	readyAfter := fs.Duration("ready-after", 0, "how long /readyz says 503")
	childPID := fs.String("child-pid", "", "file a grandchild's pid is written to")
	drain := fs.Duration("drain", 0, "how long it takes to drain after being asked to stop")
	// TestMain writes the environment out, before the test harness adds
	// variables of its own; the flag is only here so the parser accepts it.
	_ = fs.String("dump-env", "", "file the environment is written to")
	_ = fs.Parse(args[1:])

	runs := 0
	if *state != "" {
		if b, err := os.ReadFile(*state); err == nil {
			runs = len(strings.Fields(string(b)))
		}
		f, _ := os.OpenFile(*state, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintf(f, "%d\n", *port)
		f.Close()
	}

	switch *mode {
	case "exit-early":
		fmt.Fprintln(os.Stderr, "fake helper: exiting before the banner")
		os.Exit(1)
	case "exit-early-once":
		if runs == 0 {
			os.Exit(1)
		}
	case "silent", "sleep":
		time.Sleep(time.Hour)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake helper: cannot bind:", err)
		os.Exit(1)
	}
	switch *mode {
	case "wrong-banner":
		fmt.Println("hello from some other program")
	case "wrong-port":
		fmt.Printf("lens 0.0.0-test at http://localhost:%d/\n", *port+1)
	default:
		fmt.Printf("lens 0.0.0-test at http://localhost:%d/\n", *port)
	}
	fmt.Println("fake helper: second line of output")

	// The crash modes are timed from the first successful readiness check, so
	// a slow machine cannot make the helper die before it was ever running.
	var armed sync.Once
	arm := func() {}
	started := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if *mode == "unhealthy" && time.Since(started) > *after {
			http.Error(w, "unhealthy", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if *mode == "never-ready" || time.Since(started) < *readyAfter {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		arm()
		_, _ = w.Write([]byte("ready"))
	})
	go func() { _ = http.Serve(ln, mux) }()

	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	crash := func() {
		armed.Do(func() {
			time.AfterFunc(*after, func() {
				fmt.Println("fake helper: crashing")
				os.Exit(3)
			})
		})
	}
	switch *mode {
	case "crash":
		arm = crash
	case "crash-once":
		if runs == 0 {
			arm = crash
		}
	case "grandchild-crash":
		child := exec.Command(os.Args[0], "-test.run=^TestFakeHelperProcess$", "--", "serve", "--mode", "sleep")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err == nil && *childPID != "" {
			_ = os.WriteFile(*childPID, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		}
		arm = crash
	case "grandchild-drain":
		// The launcher exits on the interrupt at once; its child drains.
		child := exec.Command(os.Args[0], "-test.run=^TestFakeHelperProcess$", "--", "serve", "--mode", "drainer",
			"--marker", *marker, "--drain", drain.String())
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		_ = child.Start()
		<-sig
		os.Exit(0)
	}

	for {
		<-sig
		if *mode == "ignore-int" {
			continue
		}
		if *marker != "" {
			_ = os.WriteFile(*marker, []byte("graceful"), 0o600)
		}
		if *drain > 0 {
			time.Sleep(*drain)
		}
		os.Exit(0)
	}
}
