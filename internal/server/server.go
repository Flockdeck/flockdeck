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

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/webui"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// tokenCookie carries the session token once the window has loaded, so asset
// and WebSocket requests do not have to repeat it in every URL.
const tokenCookie = "flockdeck_token"

// stateInterval is the shortest gap between two state pushes made for the
// agents' own account. They produce output continuously; the tab bar does not
// need to be rebuilt for every chunk.
//
// Ten a second is as fast as this is worth doing. What it carries is a status
// word, a detail line and a few counts, and nobody can read those changing
// faster than that — while each one costs the window a parse and a full redraw
// of the tab bar, every pane header and the summary, taken from the same thread
// that is drawing the terminals. It used to be twenty-five a second because the
// same number decided how long a person waited to see their own click; now that
// those are separate, this one can be set on its own merits.
const stateInterval = 100 * time.Millisecond

// askedInterval is the same for a change somebody just asked for. It is a
// floor rather than a pace: it exists only so that a client sending commands
// as fast as it can cannot spin the push loop, and is short enough that
// nobody sees it.
const askedInterval = 5 * time.Millisecond

// Server serves the front end and the live connections behind it.
type Server struct {
	ws    *workspace.Workspace
	token string

	ln   net.Listener
	http *http.Server

	mu      sync.Mutex
	clients map[*controlClient]struct{}

	// lastState is the encoded snapshot that was last broadcast, kept so an
	// unchanged one is not sent again. broadcastState sets it and a window
	// connecting empties it, both on the workspace goroutine, which is what
	// keeps it free of a lock of its own.
	lastState []byte

	// cmds serialises every access to the workspace, which is not safe for
	// concurrent use and is now reached from many connection goroutines.
	cmds  chan func()
	dirty chan struct{}
	// asked is dirty for a change a person just made, which is held back for
	// far less: they are watching for it, and there are only ever a few.
	asked  chan struct{}
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

	// The agent catalog as it was last built, which project it was built for,
	// and when. Every snapshot carries it and snapshots are built ten times a
	// second, while working it out means reading a file and searching PATH
	// once per agent; agentProbeInterval is how long an answer stands. Like
	// prefs, these are touched only on the workspace goroutine.
	agents        agentCatalog
	agentsRoot    string
	agentsAt      time.Time
	agentsProbing bool

	// detached, when set, means the application should keep running after its
	// last window closes so the agents carry on.
	detached atomic.Bool

	// OnLastClientGone is called when the final window closes, so the
	// application can decide whether to shut down.
	OnLastClientGone func()
	// OnQuit is called when a shutdown is requested from the interface or by
	// another launch of the binary.
	OnQuit func()
	// OnRestart is called when the interface asks to come back up on a newly
	// downloaded version. It is separate from OnQuit because the shutdown has
	// to know whether to start the program again once it has replaced it.
	OnRestart func()

	// update is the release waiting to be applied, if one has been downloaded.
	// It is read on every snapshot and written by whatever is watching for
	// releases, so it is held as a pointer that is swapped rather than a
	// struct that is edited.
	update atomic.Pointer[UpdateView]
}

// UpdateView is a downloaded release as the interface shows it.
type UpdateView struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
	URL     string `json:"url,omitempty"`
}

// SetUpdate records that a release has been staged and is ready to be applied
// by a restart, so the next snapshot tells the windows about it.
func (s *Server) SetUpdate(u *UpdateView) { s.update.Store(u) }

// Update returns the staged release, or nil when there is none.
func (s *Server) Update() *UpdateView { return s.update.Load() }

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
		asked:   make(chan struct{}, 1),
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

// wakeAsked is Wake for a change a window asked for, which is worth telling the
// windows about sooner than the agents' own comings and goings.
func (s *Server) wakeAsked() {
	select {
	case s.asked <- struct{}{}:
	default:
	}
}

// pushLoop turns change notifications into state broadcasts.
//
// The interval is a rate limit rather than a delay: a change that arrives after
// a quiet moment goes out at once, and only the ones treading on its heels wait
// for it. Delaying every change instead put the interval on the end of every
// split, close, zoom and tab switch, which is the part of the interface a
// person is watching for. Nothing is lost by broadcasting early — each flag is
// a single slot, so a change made during the wait is still pending afterwards
// and gets a broadcast of its own.
//
// Which interval applies depends on what caused the change. Holding the agents'
// chatter to forty milliseconds is the whole point of having one; holding a
// split or a tab switch to it is not, and with several agents talking the wait
// would otherwise land on every one of them, since the chatter keeps the last
// broadcast recent. So a change somebody asked for is answered on its own, much
// shorter, floor.
func (s *Server) pushLoop() {
	var last time.Time
	for {
		asked := false
		// An asked-for change takes precedence over chatter pending at the
		// same moment, which a plain select would decide by coin toss.
		select {
		case <-s.closed:
			return
		case <-s.asked:
			asked = true
		default:
			select {
			case <-s.closed:
				return
			case <-s.asked:
				asked = true
			case <-s.dirty:
			}
		}
		if !s.holdTurn(asked, last) {
			return
		}
		last = time.Now()
		s.broadcastState()
	}
}

// holdTurn waits until a pending change may go out. It reports false if the
// server closed while it waited.
//
// A chatter wait is cut short by somebody asking for something part way
// through, which is the point of the whole arrangement: the wait exists to
// spare the window work it does not need, not to keep a person looking at a
// pane they have already closed.
func (s *Server) holdTurn(asked bool, last time.Time) bool {
	for {
		floor := stateInterval
		if asked {
			floor = askedInterval
		}
		wait := floor - time.Since(last)
		if wait <= 0 {
			return true
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
			return true
		case <-s.asked:
			timer.Stop()
			asked = true
		case <-s.closed:
			timer.Stop()
			return false
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
