// Command graphfixture serves a front end of Flockdeck and a fake control socket
// that plays one of the five week-10 graph fixtures, so the page can be looked at
// and tested without a workspace. Development aid, not part of the product.
//
// The front end is the one the binary carries. The socket
// greets a window with the key table, a state push built from the fixture's
// panes, and answers graphWatch with the fixture's graph message. It answers
// paneSend and the conversation commands with canned replies.
//
// It starts no workspace and reads no configuration. It binds loopback on a port
// the system chooses and wants a random token in the first request, which it
// prints in the link. It never opens a path in the configuration directory.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/jmwri/flockdeck/internal/webui"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "graphfixture:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("graphfixture", flag.ContinueOnError)
	fl.SetOutput(stderr)
	which := fl.String("fixture", "1", "the fixture to play: 1 to 5, or its file name")
	dir := fl.String("fixtures", "", "the directory of the fixtures; found above the working directory by default")
	list := fl.Bool("list", false, "list the fixtures and stop")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		d, err := findFixtureDir(wd)
		if err != nil {
			return err
		}
		*dir = d
	}
	if *list {
		names, err := fixtureNames(*dir)
		if err != nil {
			return err
		}
		for _, n := range names {
			fmt.Fprintln(stdout, n)
		}
		return nil
	}
	fx, err := loadFixture(*dir, *which)
	if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		return err
	}
	ln, err := listen()
	if err != nil {
		return err
	}
	s := &fixtureServer{fx: fx, files: webui.FS(), token: token}
	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(stdout, "fixture: %s\nopen: http://%s/?t=%s\n", fx.name, ln.Addr(), token)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
