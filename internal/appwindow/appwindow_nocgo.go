//go:build !cgo && !windows

// Package appwindow, built without cgo and not on Windows: Wails' Linux
// backend needs cgo to compile at all (its macOS one does too, though a
// no-cgo macOS build is not one this project actually produces), and the
// self-hosted Docker image is deliberately CGO_ENABLED=0 for a small static
// binary with no GTK runtime in the container. Windows is excluded from
// this build regardless of cgo -- its backend reaches WebView2 through raw
// syscalls and needs none, and every real Windows release already builds
// with CGO_ENABLED=0 the same way this image does, so folding it in here
// too silently gave every Windows release this file's always-failing Open
// instead of a real window (see appwindow.go's own comment on that). Open
// always fails here, but nothing in the one build this now actually applies
// to calls it: -no-window and -detach, the only ways that image runs
// Flockdeck, skip Open entirely (see showWindow in main.go).
package appwindow

import "errors"

// Config mirrors the cgo build's Config so callers compile unchanged; its
// fields go unused here since Open never gets far enough to read them.
type Config struct {
	Name        string
	Description string
	Icon        []byte
}

// Window is never actually constructed in this build: Open always returns
// nil for it. The type exists so callers compile unchanged.
type Window struct{}

// errNoCGO is returned by Open. It names the actual cause -- not "no
// window support," which would be true of -no-window too, but specifically
// that this binary cannot open one at all, however it's asked to.
var errNoCGO = errors.New("appwindow: this build has no cgo, so it cannot open a native window (built for a headless server)")

func Open(Config, string) (*Window, error) { return nil, errNoCGO }

func (w *Window) Run() error { return nil }

func (w *Window) Close() {}
