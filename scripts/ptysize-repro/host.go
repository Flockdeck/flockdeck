//go:build ignore

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/aymanbagabas/go-pty"
)

func main() {
	cols, _ := strconv.Atoi(os.Args[2])
	rows, _ := strconv.Atoi(os.Args[3])
	p, err := pty.New()
	if err != nil {
		panic(err)
	}
	defer p.Close()
	if err := p.Resize(cols, rows); err != nil {
		panic(err)
	}
	cmd := p.Command(os.Args[1])
	cmd.Env = append(os.Environ(), "GARBLE_COPY="+os.Args[4]+".direct")
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	f, _ := os.Create(os.Args[4])
	done := make(chan struct{})
	go func() { io.Copy(f, p); close(done) }()
	cmd.Wait()
	time.Sleep(500 * time.Millisecond)
	p.Close()
	<-done
	f.Close()
	fmt.Println("ok")
}
