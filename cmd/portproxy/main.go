// Command portproxy is the sidecar half of giving self-hosted service mode a
// fixed, routable address.
//
// Flockdeck's own server always binds 127.0.0.1 on a port chosen at random
// each start, with no bind-address flag and no way to pin the port (see
// internal/server.New) -- deliberately, since a desktop install has no
// business claiming a fixed port on someone's machine. That is fine for a
// desktop app, whose window dials its own process directly, but it means a
// Kubernetes Service or Ingress has nothing stable to route to: the port
// changes every restart, and nothing outside the pod can be told what it is
// ahead of time.
//
// Containers in one pod share a network namespace, so a second, tiny
// container that listens on a fixed port and forwards every byte of every
// connection to whatever port flockdeck actually bound this run gives a
// Service exactly the fixed target it needs, without changing a line of
// flockdeck's own server code. That is all this program does. It discovers
// flockdeck's real address the same way a second `flockdeck` launch and
// docker/healthcheck.sh already do -- by reading the instance record
// flockdeck writes at start-up ($HOME/.config/flockdeck/instance.json,
// internal/store.SaveInstance) -- and re-reads it on every connection, so a
// restart that lands on a different port is picked up without this sidecar
// itself needing to be restarted.
//
// It is deliberately not an HTTP-aware reverse proxy. flockdeck's own server
// already speaks HTTP, including the WebSocket upgrades /ws/control and
// /ws/pty use, and a byte-for-byte TCP relay forwards all of it -- headers,
// chunked bodies, upgrade handshakes, the auth token in the query string --
// completely unchanged, without this program having to parse, buffer, or
// trust any of it. That also means it carries no authorization logic of its
// own: whoever connects through it still needs the token from the instance
// record, exactly as they would today over `kubectl port-forward` straight
// to flockdeck's own loopback port. The sidecar only fixes the address; it
// changes nothing about who is allowed to use it.
//
// Built as a second binary in the same image as flockdeck itself (see the
// repository's Dockerfile) rather than shipped as its own container image,
// so it rides the same build, the same base-image hardening, the same CVE
// scan and the same cosign signature as flockdeck already gets -- one more
// image to pull, scan and trust independently would be a worse trade for a
// program this small.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

func main() {
	listen := flag.String("listen", ":8080", "fixed `address` to listen on and forward every connection from")
	flag.Parse()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("portproxy: listen on %s: %v", *listen, err)
	}
	log.Printf("portproxy: listening on %s, forwarding each connection to whatever port flockdeck's own instance record names", *listen)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	serve(ctx, ln)
}

// serve accepts connections until ctx is done (which also closes ln, ending
// Accept with an error this loop recognises as shutdown rather than a fault
// worth logging).
func serve(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("portproxy: accept: %v", err)
			continue
		}
		go handle(conn)
	}
}

// dialTimeout bounds how long connecting to flockdeck's own loopback port is
// given. The two share a network namespace and there is no real network
// between them, so a moment is generous; it exists to fail a stuck dial
// rather than to accommodate a slow one.
const dialTimeout = 5 * time.Second

// handle serves one accepted connection: find flockdeck's current address
// and hand off to handleConn.
func handle(client net.Conn) {
	defer client.Close()
	target, err := targetAddr()
	if err != nil {
		log.Printf("portproxy: %v", err)
		return
	}
	handleConn(client, target)
}

// handleConn dials target and relays bytes both ways until either side is
// done. Split out from handle so a test can exercise the actual relay
// against a stand-in upstream without an instance record on disk.
func handleConn(client net.Conn, target string) {
	upstream, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		log.Printf("portproxy: dial %s: %v", target, err)
		return
	}
	defer upstream.Close()
	proxy(client, upstream)
}

// targetAddr is the host:port flockdeck actually bound this run, discovered
// from the instance record it writes at start-up -- the same record
// docker/healthcheck.sh and a second `flockdeck` launch (internal/server's
// own attach probe) already read to find it. Reading it fresh on every call,
// rather than once and cached, is what lets this sidecar keep working across
// a restart that lands flockdeck on a different port without itself needing
// to notice or be restarted.
func targetAddr() (string, error) {
	inst, err := store.LoadInstance()
	if err != nil {
		return "", fmt.Errorf("read flockdeck's instance record: %w", err)
	}
	if inst == nil || inst.URL == "" {
		return "", errors.New("no instance record yet -- flockdeck has not started, or is between restarts")
	}
	return hostFromInstanceURL(inst.URL)
}

// hostFromInstanceURL pulls the host:port out of the URL flockdeck's own
// Server.BaseURL wrote to the instance record ("http://127.0.0.1:<port>",
// see internal/server.Server.BaseURL) -- split out from targetAddr so the
// parsing itself can be tested without an instance record on disk.
func hostFromInstanceURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse instance url %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("instance url %q has no host", raw)
	}
	return u.Host, nil
}

// proxy relays raw bytes both ways between a and b until both directions
// have finished, half-closing each side's write direction as its own copy
// ends rather than waiting for the other direction too -- so the last bytes
// of an HTTP response, or a WebSocket close frame, are not held back behind
// a request body or a client that just stopped reading.
func proxy(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { copyHalf(b, a); done <- struct{}{} }()
	go func() { copyHalf(a, b); done <- struct{}{} }()
	<-done
	<-done
}

// copyHalf copies src to dst until src reaches EOF or either side errors,
// then half-closes dst's write side so its peer sees the end of the stream
// promptly. A connection that cannot half-close (nothing on the path here
// except *net.TCPConn) is closed outright instead, which is what the other
// copyHalf goroutine's own read is about to see fail anyway.
func copyHalf(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	} else {
		_ = dst.Close()
	}
}
