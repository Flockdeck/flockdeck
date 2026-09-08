// Package server exposes the workspace to the browser front end.
//
// It listens on the loopback interface only and requires a token that is
// generated per run and handed to the window in its URL, so nothing else on
// the machine can drive the agents through it.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/agent-wrapper/internal/store"
	"github.com/jmwri/agent-wrapper/internal/webui"
	"github.com/jmwri/agent-wrapper/internal/workspace"
)

// tokenCookie carries the session token once the window has loaded, so asset
// and WebSocket requests do not have to repeat it in every URL.
const tokenCookie = "agent_wrapper_token"

// stateDebounce coalesces bursts of session activity into a single state push.
// Agents produce output continuously; the tab bar does not need to be rebuilt
// for every chunk.
const stateDebounce = 40 * time.Millisecond

// Server serves the front end and the live connections behind it.
type Server struct {
	ws    *workspace.Workspace
	token string

	ln   net.Listener
	http *http.Server

	mu      sync.Mutex
	clients map[*controlClient]struct{}

	// cmds serialises every access to the workspace, which is not safe for
	// concurrent use and is now reached from many connection goroutines.
	cmds   chan func()
	dirty  chan struct{}
	closed chan struct{}
	// gitNow asks the git loop for an out-of-turn refresh, so a window that
	// has just opened does not have to wait out the interval for its branch
	// labels.
	gitNow chan struct{}
	once   sync.Once

	// prefs is what the interface remembers about this person rather than
	// about a workspace. It is read and written only on the workspace
	// goroutine, which is what keeps it free of a lock of its own.
	prefs store.Prefs

	// detached, when set, means the application should keep running after its
	// last window closes so the agents carry on.
	detached atomic.Bool

	// OnLastClientGone is called when the final window closes, so the
	// application can decide whether to shut down.
	OnLastClientGone func()
	// OnQuit is called when a shutdown is requested from the interface or by
	// another launch of the binary.
	OnQuit func()
}

// New starts a server for the workspace on a free loopback port.
func New(ws *workspace.Workspace) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		ln.Close()
		return nil, fmt.Errorf("generate token: %w", err)
	}

	s := &Server{
		ws:      ws,
		prefs:   store.LoadPrefs(),
		token:   hex.EncodeToString(raw),
		ln:      ln,
		clients: map[*controlClient]struct{}{},
		cmds:    make(chan func(), 64),
		dirty:   make(chan struct{}, 1),
		gitNow:  make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/assets/", http.StripPrefix("/assets/", s.authFiles(webui.FS())))
	mux.HandleFunc("/ws/control", s.handleControl)
	mux.HandleFunc("/ws/pty", s.handlePTY)
	mux.HandleFunc("/help.json", s.handleHelp)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/open", s.handleOpen)
	mux.HandleFunc("/quit", s.handleQuit)

	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	go s.runLoop()
	go s.pushLoop()
	go s.gitLoop()
	s.installSpawnHandler()
	s.installContextHandler()

	return s, nil
}

// URL is the address the window should open, including the token that
// authorises it.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?t=%s", s.ln.Addr().String(), s.token)
}

// Addr returns the listening address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Token returns the per-run token.
func (s *Server) Token() string { return s.token }

// Wake tells the server that workspace state changed and clients should be
// updated. It is safe to call from any goroutine and never blocks.
func (s *Server) Wake() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

// pushLoop coalesces change notifications into state broadcasts.
func (s *Server) pushLoop() {
	for {
		select {
		case <-s.closed:
			return
		case <-s.dirty:
			// Wait out the rest of the burst before rebuilding the snapshot.
			select {
			case <-time.After(stateDebounce):
			case <-s.closed:
				return
			}
			s.broadcastState()
		}
	}
}

// gitStatusInterval is how often each pane's checkout is re-examined. Git is
// cheap here but not free, and uncommitted work does not appear that fast.
const gitStatusInterval = 15 * time.Second

// RefreshGitNow asks the git loop to re-examine the checkouts straight away.
// It is safe to call from any goroutine and never blocks; a request made while
// one is already pending is folded into it.
func (s *Server) RefreshGitNow() {
	select {
	case s.gitNow <- struct{}{}:
	default:
	}
}

// gitLoop keeps the per-pane git summaries current. Doing it here rather than
// per caller keeps one refresh running at a time: each one shells out to git
// once per distinct checkout and waits for them all.
func (s *Server) gitLoop() {
	tick := time.NewTicker(gitStatusInterval)
	defer tick.Stop()
	// Fill them in shortly after startup rather than making the first window
	// wait fifteen seconds for a branch label.
	first := time.After(750 * time.Millisecond)
	for {
		select {
		case <-s.closed:
			return
		case <-first:
			s.ws.RefreshGit(s.do)
		case <-s.gitNow:
			s.ws.RefreshGit(s.do)
		case <-tick.C:
			// Nothing is reading the branch labels while every window is
			// closed, and a detached run can sit like that for hours: polling
			// git over every checkout the whole time buys nobody anything.
			// A window that opens asks for a refresh of its own.
			if s.ClientCount() == 0 {
				continue
			}
			s.ws.RefreshGit(s.do)
		}
	}
}

// authorised reports whether a request carries the token, either as the query
// parameter used on first load or as the cookie set from it.
func (s *Server) authorised(r *http.Request) bool {
	if t := r.URL.Query().Get("t"); t != "" && s.tokenMatches(t) {
		return true
	}
	if c, err := r.Cookie(tokenCookie); err == nil {
		return s.tokenMatches(c.Value)
	}
	return false
}

func (s *Server) tokenMatches(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

// handleIndex serves the page, promoting a token in the URL to a cookie so
// that later requests from the page carry it automatically.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Only a token that is actually ours is promoted. The request may have
	// been authorised by an existing cookie while carrying a stale `t` from a
	// bookmarked URL of an earlier run, and storing that would replace a
	// working cookie with one that no longer opens anything.
	if t := r.URL.Query().Get("t"); s.tokenMatches(t) {
		http.SetCookie(w, &http.Cookie{
			Name:     tokenCookie,
			Value:    t,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
	}

	data, err := fs.ReadFile(webui.FS(), "index.html")
	if err != nil {
		http.Error(w, "missing front end", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// authFiles serves embedded assets to authorised requests.
func (s *Server) authFiles(fsys fs.FS) http.Handler {
	files := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// The page names these assets without a version in the path, and the
		// window keeps a browser profile of its own from one run to the next,
		// so a cached app.js would outlive the binary it came with and be left
		// talking to an upgraded server. Embedded files carry no modification
		// time for the browser to revalidate against either, and fetching them
		// again over loopback costs nothing.
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}

// Close shuts the server down.
func (s *Server) Close() error {
	s.once.Do(func() { close(s.closed) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

// BaseURL is the address without the token, for the health and control
// endpoints a second launch uses.
func (s *Server) BaseURL() string { return "http://" + s.ln.Addr().String() }

// pid is the running process id, reported by the health endpoint.
func pid() int { return os.Getpid() }

// queryEscape percent-encodes a query parameter value.
func queryEscape(s string) string { return url.QueryEscape(s) }

// ClientCount reports how many windows are currently connected.
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}
