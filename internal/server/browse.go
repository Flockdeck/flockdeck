package server

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/store"
)

// dirEntry is one directory offered by the project picker.
type dirEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	IsRepo bool   `json:"isRepo"`
	Hidden bool   `json:"hidden"`
}

type browseMsg struct {
	Type    string     `json:"type"`
	Path    string     `json:"path"`
	Parent  string     `json:"parent,omitempty"`
	IsRepo  bool       `json:"isRepo"`
	Entries []dirEntry `json:"entries"`
	Places  []dirEntry `json:"places"`
	Error   string     `json:"error,omitempty"`
}

type recentsMsg struct {
	Type  string       `json:"type"`
	Items []recentView `json:"items"`
}

type recentView struct {
	Root   string `json:"root"`
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
	Open   bool   `json:"open"`
}

// browse lists the directories inside path so the picker can navigate the file
// system. A browser cannot give us a real directory path — the File System
// Access API hands out opaque handles — so choosing a project has to be served
// from this side.
func (s *Server) browse(c *controlClient, path string) {
	go func() {
		msg := browseMsg{Type: "browse"}

		if path == "" {
			if home, err := os.UserHomeDir(); err == nil {
				path = home
			} else {
				path = "."
			}
		}
		abs, err := filepath.Abs(expandHome(path))
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		msg.Path = abs
		msg.Places = places()
		// The parent is filled in before the listing is attempted. A directory
		// that cannot be read — one belonging to another user, say — is exactly
		// where the picker most needs its way back out, and leaving this until
		// afterwards disabled the up control and stranded whoever walked in.
		if parent := filepath.Dir(abs); parent != abs {
			msg.Parent = parent
		}

		entries, err := os.ReadDir(abs)
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		msg.IsRepo = isRepoDir(abs)

		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(abs, name)
			if !e.IsDir() {
				// A listing describes the entry itself, not what it leads to:
				// a symlink to a project reads as a symlink, and a Windows
				// directory junction as neither a file nor a directory. Only
				// following them says whether they are somewhere to open, so
				// everything that is not plainly a file is stat-ed -- which
				// costs nothing on entries that were about to be dropped.
				if e.Type().IsRegular() {
					continue
				}
				if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
					continue
				}
			}
			msg.Entries = append(msg.Entries, dirEntry{
				Name:   name,
				Path:   full,
				IsRepo: isRepoDir(full),
				Hidden: strings.HasPrefix(name, ".") || hiddenOnDisk(e),
			})
		}
		sort.Slice(msg.Entries, func(i, j int) bool {
			// Repositories first: they are what someone picking a project wants.
			if msg.Entries[i].IsRepo != msg.Entries[j].IsRepo {
				return msg.Entries[i].IsRepo
			}
			if msg.Entries[i].Hidden != msg.Entries[j].Hidden {
				return !msg.Entries[i].Hidden
			}
			return strings.ToLower(msg.Entries[i].Name) < strings.ToLower(msg.Entries[j].Name)
		})
		c.sendJSON(msg)
	}()
}

// expandHome reads a leading ~ as the home directory. That is how a path is
// written in every shell the person typing it uses, and what the folder
// browser's box is handed when one is pasted in; taken as it stands, it named
// a directory called "~" inside wherever flockdeck happened to be started.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// isRepoDir reports whether dir is the top of a git repository. Checking for
// the .git entry directly avoids running git once per listed directory.
func isRepoDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	return false
}

// places are the shortcuts offered alongside the listing: the home directory,
// and on Windows the available drives.
func places() []dirEntry {
	var out []dirEntry
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, dirEntry{Name: "Home", Path: home})
		for _, name := range []string{"Documents", "Projects", "code", "src", "repos", "dev"} {
			p := filepath.Join(home, name)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				out = append(out, dirEntry{Name: name, Path: p})
			}
		}
	}
	if runtime.GOOS == "windows" {
		for c := 'C'; c <= 'Z'; c++ {
			drive := string(c) + `:\`
			if fi, err := os.Stat(drive); err == nil && fi.IsDir() {
				out = append(out, dirEntry{Name: string(c) + ":", Path: drive})
			}
		}
	} else {
		out = append(out, dirEntry{Name: "/", Path: "/"})
	}
	return out
}

// recents answers the picker's request for previously opened projects.
func (s *Server) recents(c *controlClient) {
	open := map[string]bool{}
	type result struct {
		list []store.Project
		err  error
	}
	done := make(chan result, 1)
	s.do(func() {
		for _, p := range s.ws.Projects() {
			open[p.Root] = true
		}
		list, err := store.Recents()
		done <- result{list: list, err: err}
	})

	go func() {
		var res result
		select {
		case res = <-done:
		case <-s.closed:
			return
		}
		if res.err != nil {
			// A picker with nothing in it looks exactly like never having
			// opened a project before, so an unreadable list has to say so
			// rather than pass for an empty one.
			c.notify("could not read the recent projects: "+res.err.Error(), true)
		}
		msg := recentsMsg{Type: "recents"}
		for _, p := range res.list {
			fi, err := os.Stat(p.Root)
			msg.Items = append(msg.Items, recentView{
				Root:   p.Root,
				Name:   filepath.Base(p.Root),
				Exists: err == nil && fi.IsDir(),
				Open:   open[p.Root],
			})
		}
		c.sendJSON(msg)
	}()
}
