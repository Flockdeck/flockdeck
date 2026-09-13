// Package server exposes the workspace to the browser front end.
//
// It listens on the loopback interface only and requires a token that is
// generated per run and handed to the window in its URL, so nothing else on
// the machine can drive the agents through it.
package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/spend"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/webui"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// tokenCookie carries the session token once the window has loaded, so asset
// and WebSocket requests do not have to repeat it in every URL. It is the stem
// of the cookie's name rather than the whole of it: see cookieName.
const tokenCookie = "flockdeck_token"

// cookieName is the cookie this instance keeps its token in, named for the
// port it listens on.
//
// A cookie belongs to a host and takes no notice of the port. Two instances on
// 127.0.0.1 -- a -solo run beside the usual one, whose windows share the one
// browser profile in the state directory -- therefore wrote the same cookie,
// and the second window to load replaced the first one's token with its own.
// The first window carried on only until it next opened a socket, for a new
// pane or a reconnect, and was then turned away by its own instance.
func (s *Server) cookieName() string {
	return tokenCookie + "_" + strconv.Itoa(s.ln.Addr().(*net.TCPAddr).Port)
}

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
	// OnDetach is called when the instance becomes detached -- from a window,
	// or by -detach at start -- once each time it goes from attached to
	// detached, and never on the workspace goroutine. It is how the application
	// lets go of what tied it to the way it was started: on Windows, the console
	// a -no-window run was launched from, whose closing would otherwise end it.
	OnDetach func()

	// update is the release waiting to be applied, if one has been downloaded.
	// It is read on every snapshot and written by whatever is watching for
	// releases, so it is held as a pointer that is swapped rather than a
	// struct that is edited.
	update atomic.Pointer[UpdateView]

	// paneLookup and usageRefresh are the package variables of the same names,
	// read once as the server is made. A test shortens them for the server it
	// makes, and a goroutine an earlier test's server left running would
	// otherwise be reading them while the next test writes them.
	paneLookup, usageRefresh time.Duration

	// mux is every route the window uses. It is kept so that the same routes
	// can be served a second time, to windows reached through the relay.
	mux *http.ServeMux
	// remote is remote access, when this instance has any. See remote.go.
	remote atomic.Pointer[remoteHolder]

	// book is what the panes' agents have said they spent. See spend.go.
	book *spend.Book

	// saveFailShown records that the windows have been told the timed layout
	// save is failing, so a save that fails every half minute is reported once
	// rather than every time, until one works again. It is a flag rather than
	// the error it was told: a failed save names the temporary file it could
	// not rename, which is a new name every time. Only the workspace goroutine
	// touches it.
	saveFailShown bool
}

// UpdateView is a downloaded release as the interface shows it.
type UpdateView struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
	URL     string `json:"url,omitempty"`
}

// SetUpdate records that a release has been staged and is ready to be applied
// by a restart, and has the windows told.
//
// Nothing else would tell them. A snapshot goes out when something changes,
// and a release is staged in the background, hours into a run, most likely
// while the agents sit waiting and nobody is touching the window -- which is
// then not told until something unrelated happens.
func (s *Server) SetUpdate(u *UpdateView) {
	s.update.Store(u)
	s.Wake()
}

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

		paneLookup:   paneLookup,
		usageRefresh: usageRefresh,
		book:         spend.NewBook(),
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
	mux.HandleFunc("/remote/reload", s.handleRemoteReload)
	s.mux = mux

	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	go s.runLoop()
	go s.pushLoop()
	go s.gitLoop()
	go s.usageLoop()
	go s.saveLoop()
	s.installSpawnHandler()
	s.installContextHandler()
	s.installUsageHandler()

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
// chatter to stateInterval is the whole point of having one; holding a
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

// gitLoop keeps the per-pane git summaries current.
//
// Each refresh runs on a goroutine of its own. A refresh waits for every
// checkout it asked about, and one that hangs is waited on until its deadline:
// run here, that held back the next refresh of every other checkout too, so a
// commit made in one pane went uncounted in its header while an unrelated
// checkout was stuck. The workspace does not ask about a checkout still being
// read, so refreshes that overlap do not pile git processes onto the slow one.
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
			go s.ws.RefreshGit(s.do)
		case <-s.gitNow:
			go s.ws.RefreshGit(s.do)
		case <-tick.C:
			// Nothing is reading the branch labels while every window is
			// closed, and a detached run can sit like that for hours: polling
			// git over every checkout the whole time buys nobody anything.
			// A window that opens asks for a refresh of its own.
			if s.ClientCount() == 0 {
				continue
			}
			go s.ws.RefreshGit(s.do)
		}
	}
}

// usageRefresh is how often a snapshot is rebuilt while a window is open, so
// that each pane's CPU and memory are read again. It matches how often the
// session package reads the process table at most; a variable so a test need
// not wait it out.
var usageRefresh = 5 * time.Second

// usageLoop has the snapshot rebuilt on a timer while a window is open.
//
// A pane's figures are read only as a snapshot is built, and a snapshot is
// otherwise built only when something changes. An agent that had just stopped
// was sent as idle with its fifteen-second CPU average still high, and nothing
// built another: its header showed that share, marked hot, for as long as the
// rest of the workspace stayed quiet. A rebuild whose rounded figures draw the
// same as before is not sent (see shownUsage), so this costs a snapshot and a
// comparison every few seconds, and sends only what a window would draw
// differently.
func (s *Server) usageLoop() {
	tick := time.NewTicker(s.usageRefresh)
	defer tick.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-tick.C:
			if s.ClientCount() > 0 {
				s.Wake()
			}
		}
	}
}

// layoutSaveInterval is how often every open project's layout is written while
// the app runs. A variable so a test need not wait it out.
var layoutSaveInterval = 30 * time.Second

// saveLoop writes every open project's layout on a timer.
//
// Layouts were written only as the app stopped, as a project closed, and as a
// window detached or reloaded. A run that was killed, crashed or lost its
// machine to a power cut came back as the layout it had started with: every
// tab opened, pane split and agent spawned since was gone, which is exactly
// the work a restore is there to bring back. A layout that has not changed is
// not rewritten (store.writeAtomic compares first), so a quiet workspace costs
// an encoding and a comparison per project.
//
// Which projects are open is left to the save at the end, as it always was.
// A -new run puts back the list it skipped only once that save has been
// made, and a list written here in between would be all a crash left of it.
func (s *Server) saveLoop() {
	tick := time.NewTicker(layoutSaveInterval)
	defer tick.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-tick.C:
			s.do(s.saveLayouts)
		}
	}
}

// saveLayouts is one turn of saveLoop, run on the workspace goroutine.
//
// Work queued before the server closed can still be run after it, and by then
// the app is saving for the last time and closing the panes on another
// goroutine. A save here would be reading the workspace while that one tore
// it down, for layouts the last save writes anyway, so it stands aside.
func (s *Server) saveLayouts() {
	select {
	case <-s.closed:
		return
	default:
	}
	// A save that keeps failing — a state folder that cannot be written to,
	// a disk with no room left — used to be heard of only as the app stopped,
	// on a terminal the window had long since hidden. The windows are told
	// while there is still something to be done about it: once when it starts
	// failing, and again if it fails after having worked. It counts as told
	// only if a window was there to be told.
	err := s.ws.SaveLayouts()
	if err == nil {
		s.saveFailShown = false
		return
	}
	if !s.saveFailShown && s.ClientCount() > 0 {
		s.notifyAll("the layout could not be saved, and it is tried again every half minute; if this goes on, check that the disk has room and that Flockdeck's state folder can be written to: "+err.Error(), true)
		s.saveFailShown = true
	}
}

// authorised reports whether a request carries the token, either as the query
// parameter used on first load or as the cookie set from it — or came through
// the relay, which is authorisation of another kind (see remote.go).
func (s *Server) authorised(r *http.Request) bool {
	if fromRemote(r) {
		return true
	}
	if t := r.URL.Query().Get("t"); t != "" && s.tokenMatches(t) {
		return true
	}
	if c, err := r.Cookie(s.cookieName()); err == nil {
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
			Name:     s.cookieName(),
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
	// Nothing frames this page legitimately, locally or through the relay. A
	// page on another local port could, though -- it counts as the same site,
	// so the cookie goes with the request -- and then lay a decoy over the
	// window to have the person click Quit or type into an agent for it. The
	// sockets refuse such a page; this keeps it from borrowing the window.
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
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
		// Through the relay the window is often a phone on a metered link, and
		// no-store means every page load fetches the front end again: a
		// megabyte of script, most of it the terminal emulator. Script and
		// styles compress to a fraction, so they are sent that way when the
		// browser says it can take it. Over loopback it would buy nothing.
		if fromRemote(r) && acceptsGzip(r) && compressible(r.URL.Path) {
			name := r.URL.Path
			if data, ok := gzipped(name, func() ([]byte, error) { return fs.ReadFile(fsys, name) }); ok {
				if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Vary", "Accept-Encoding")
				_, _ = w.Write(data)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

// compressible reports whether a file is text worth gzipping. An image is
// compressed already.
func compressible(name string) bool {
	switch path.Ext(name) {
	case ".js", ".css", ".html", ".json", ".svg":
		return true
	}
	return false
}

// acceptsGzip reports whether a request says it can take a gzipped reply. A
// coding given a quality of zero -- "gzip;q=0" -- is one the client refuses.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
			if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); strings.EqualFold(k, "q") && err == nil && q == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// gzippedAssets holds what has been compressed, by key. Everything given to it
// is compiled in and never changes -- the assets, the help pages -- so each is
// compressed once, on its first request through the relay, and the answer kept
// for the life of the process.
var gzippedAssets sync.Map

// gzipped returns what load gives, gzipped, compressing it the first time key
// is asked for. It reports false when there is nothing to load.
func gzipped(key string, load func() ([]byte, error)) ([]byte, bool) {
	if data, ok := gzippedAssets.Load(key); ok {
		return data.([]byte), true
	}
	plain, err := load()
	if err != nil {
		return nil, false
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(plain)
	if zw.Close() != nil {
		return nil, false
	}
	gzippedAssets.Store(key, buf.Bytes())
	return buf.Bytes(), true
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
