//go:build ignore

package main

import (
	"fmt"
	"os"
	"time"
)

// Static transcript, then only the status line is redrawn: what a busy agent does.
func main() {
	cp, _ := os.Create(os.Getenv("GARBLE_COPY"))
	w := func(s string) { os.Stdout.WriteString(s); cp.WriteString(s) }
	for k := 0; k < 22; k++ {
		w(fmt.Sprintf("  row %02d: your environment gives you and the PR body ends with the final text marker, wethout yhat\r\n", k))
	}
	w("* Imagining... (0m 00s)\r\n> ")
	for i := 1; i < 40; i++ {
		w(fmt.Sprintf("\x1b[1A\r\x1b[2K* Imagining... (0m %02ds · ↓ %d.%dk tokens)\r\n> ", i, i/10, i%10))
		time.Sleep(15 * time.Millisecond)
	}
	cp.Close()
}
