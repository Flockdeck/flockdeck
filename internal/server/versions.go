package server

import (
	"context"
	"strings"
	"time"
)

// VersionView is one published release as the version picker shows it: not
// the whole of what selfupdate.Release carries, only what a person choosing
// between them needs.
type VersionView struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
	URL     string `json:"url,omitempty"`
	// Published is RFC 3339, or "" where it is not known.
	Published string `json:"published,omitempty"`
	// Relation says how this release compares to the version running now:
	// "current", "newer" or "older" -- what the picker's button reads
	// ("Reinstall", "Update to…" or "Roll back to…") without comparing
	// versions itself, which this package does not know how to do.
	Relation string `json:"relation"`
}

// versionsMsg answers the version picker.
type versionsMsg struct {
	Type  string        `json:"type"`
	Items []VersionView `json:"items,omitempty"`
	Error string        `json:"error,omitempty"`
}

// versionsTimeout bounds how long listing recent releases or installing one
// may take. Installing downloads an archive, so it is given the same latitude
// checkForUpdates gives OnCheckForUpdates.
const (
	listVersionsTimeout   = 30 * time.Second
	installVersionTimeout = 10 * time.Minute
)

// listVersions answers the interface's version picker: recent published
// releases, for somebody to choose one to install rather than only ever move
// to the latest. This package knows nothing of GitHub or releases -- that is
// OnListVersions, wired up by whatever started the server, the same way
// OnCheckForUpdates is.
func (s *Server) listVersions(c *controlClient) {
	if c.remote {
		c.notify("installing a specific version is set on the machine flockdeck runs on, not from a window reached through the relay", true)
		return
	}
	if s.OnListVersions == nil {
		c.sendJSON(versionsMsg{Type: "versions", Error: "listing versions is not available in this build"})
		return
	}
	go func() {
		defer s.surviveFor(c, "listing recent versions")
		ctx, cancel := context.WithTimeout(context.Background(), listVersionsTimeout)
		defer cancel()
		items, err := s.OnListVersions(ctx)
		if err != nil {
			c.sendJSON(versionsMsg{Type: "versions", Error: err.Error()})
			return
		}
		c.sendJSON(versionsMsg{Type: "versions", Items: items})
	}()
}

// installVersion answers the picker's choice: download and check the named
// release and stage it, reusing the same verified-download machinery every
// other update goes through (OnInstallVersion, wired up the same way
// OnCheckForUpdates is). What it downloads is offered through the ordinary
// update chip (state.update) once staged, so restarting onto it goes through
// the interface's existing confirmation dialog rather than one of its own.
func (s *Server) installVersion(c *controlClient, target string) {
	if c.remote {
		c.notify("installing a specific version is set on the machine flockdeck runs on, not from a window reached through the relay", true)
		return
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	if s.OnInstallVersion == nil {
		c.notify("installing a specific version is not available in this build", true)
		return
	}
	go func() {
		defer s.surviveFor(c, "installing "+target)
		ctx, cancel := context.WithTimeout(context.Background(), installVersionTimeout)
		defer cancel()
		message, isErr := s.OnInstallVersion(ctx, target)
		c.notify(message, isErr)
	}()
}
