// Command hooktest verifies end to end that a Claude pane reports its
// lifecycle back through Flockdeck's hook server: it starts one agent pane,
// types a prompt, and prints every status transition it observes.
//
// It is a development aid, not part of the product.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

func main() {
	var (
		hookBin = flag.String("hookbin", "", "path to the built flockdeck binary")
		prompt  = flag.String("prompt", "Reply with exactly the word OK and nothing else.", "prompt to send")
		watch   = flag.Duration("watch", 90*time.Second, "how long to watch for transitions")
	)
	flag.Parse()

	if *hookBin == "" {
		fmt.Fprintln(os.Stderr, "-hookbin is required: build the binary first, then point at it")
		os.Exit(2)
	}
	fi, err := os.Stat(*hookBin)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "hook binary:", err)
		os.Exit(2)
	case fi.IsDir():
		fmt.Fprintf(os.Stderr, "hook binary: %s is a directory\n", *hookBin)
		os.Exit(2)
	}

	cwd, _ := os.Getwd()
	ws, err := workspace.New(workspace.Options{Root: cwd, HookBinary: *hookBin})
	if err != nil {
		fmt.Println("workspace:", err)
		os.Exit(1)
	}
	defer ws.Close()

	if !ws.ClaudeAvailable() {
		fmt.Println("claude CLI not found")
		os.Exit(1)
	}

	ws.NewTab(session.KindClaude, cwd, "hooktest")
	p := ws.FocusedPane()
	// A tab that could not be created at all leaves no pane to read an error
	// off, so the nil case has to be reported on its own.
	if p == nil {
		fmt.Fprintln(os.Stderr, "no pane was created")
		os.Exit(1)
	}
	if p.Err != nil {
		fmt.Fprintln(os.Stderr, "pane failed to start:", p.Err)
		os.Exit(1)
	}

	fmt.Println("settings:", settingsPath(p.ID))
	dumpSettings(p.ID)

	// Let Claude finish drawing its prompt before typing.
	time.Sleep(6 * time.Second)

	fmt.Printf("\n--- typing prompt ---\n")
	_ = p.Sess.WriteString(*prompt)
	time.Sleep(300 * time.Millisecond)
	_ = p.Sess.WriteString("\r")

	start := time.Now()
	last := session.Status(-1)
	lastDetail := ""
	deadline := time.After(*watch)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()

	seen := map[session.Status]bool{}
	for {
		select {
		case <-deadline:
			dumpTail(p)
			report(seen)
			return
		case <-tick.C:
			st, detail := p.Sess.Status()
			if st != last || detail != lastDetail {
				fmt.Printf("%7.1fs  %-8s %s\n", time.Since(start).Seconds(), st, detail)
				last, lastDetail = st, detail
				seen[st] = true
			}
			if st == session.StatusExited {
				dumpTail(p)
				report(seen)
				return
			}
		}
	}
}

// dumpTail prints the end of the pane's output, which is how a stalled turn is
// told apart from a permission prompt.
func dumpTail(p *workspace.Pane) {
	_, replay, _ := p.Sess.Subscribe()
	text := string(replay)
	if len(text) > 4000 {
		text = text[len(text)-4000:]
	}
	fmt.Printf("\n--- pane output (tail) ---\n%s\n", text)
}

func report(seen map[session.Status]bool) {
	fmt.Printf("\nobserved: working=%v waiting=%v idle=%v\n",
		seen[session.StatusWorking], seen[session.StatusWaiting], seen[session.StatusIdle])
	if seen[session.StatusWorking] && seen[session.StatusIdle] {
		fmt.Println("RESULT: hooks are delivering lifecycle events")
		return
	}
	fmt.Println("RESULT: did not observe a working -> idle cycle")
}

func settingsPath(id string) string {
	dir, err := store.SessionsDir()
	if err != nil {
		return ""
	}
	return dir + string(os.PathSeparator) + id + ".settings.json"
}

func dumpSettings(id string) {
	data, err := os.ReadFile(settingsPath(id))
	if err != nil {
		fmt.Println("could not read settings:", err)
		return
	}
	fmt.Println(string(data))
}
