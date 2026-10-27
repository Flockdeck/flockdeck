package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"os/signal"

	"github.com/jmwri/flockdeck/internal/mcpserver"
)

// runMCPServer is `flockdeck mcp-server`: the Flockdeck MCP server on stdin and
// stdout. It is hidden, and an agent starts it from the mcpServers entry
// Flockdeck gave its session. Who it speaks for is the token in that entry's
// environment, issued for one pane; it takes no pane id from anywhere else.
func runMCPServer(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: flockdeck mcp-server (started by an agent; it takes no arguments)")
	}
	api, err := loopbackAPI(os.Getenv("FLOCKDECK_API"))
	if err != nil {
		return err
	}
	token := os.Getenv("FLOCKDECK_TOKEN")
	if token == "" {
		return errors.New("FLOCKDECK_TOKEN is not set; this server is started by Flockdeck for one pane")
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals()...)
	defer stop()
	return mcpserver.Run(ctx, os.Stdin, os.Stdout, mcpserver.Options{
		Backend: mcpserver.API{URL: api, Token: token},
		Version: version,
	})
}

// loopbackAPI checks the address is a plain http URL on a loopback IP. The token
// is sent to it, so an address anywhere else is refused.
func loopbackAPI(raw string) (string, error) {
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Scheme != "http" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("FLOCKDECK_API is not set to an http://127.0.0.1:port address")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() || u.Port() == "" {
		return "", errors.New("FLOCKDECK_API is not a loopback address, so the token will not be sent to it")
	}
	return "http://" + u.Host, nil
}
