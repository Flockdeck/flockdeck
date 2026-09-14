// Package server exposes the workspace to the browser front end.
//
// It listens on the loopback interface only and requires a token that is
// generated per run, so nothing else on the machine can drive the agents
// through it. The window is handed it through a one-time link (see
// WindowURL), never in the URL on its browser's command line.
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

	// links are the one-time links a window may be opened with, each with
	// when it stops working and, where the caller has said (see SetLinkFile),
	// the file on this machine it was written into as a local redirect --
	// removed the moment the link stops being any good, rather than left for
	// somebody to come across later. linkLife is windowLinkLife, read once as
	// the server is made. See WindowURL.
	linkMu   sync.Mutex
	links    map[string]linkEntry
	linkLife time.Duration

	// spent is what became of a link once it left links -- used, or run out
	// before anything did -- kept for linkFateRetention so a load that
	// arrives at it anyway can be told which, in the house style, rather than
	// a bare refusal that reads as this program being broken. See
	// serveLinkGone.
	spentMu sync.Mutex
	spent   map[string]linkFateEntry

	ln   net.Listener
	http *http.Server

	mu sync.Mutex
	// clients is every window connected, from the moment its socket opens,
	// and whether it has been handed its hello: only a window that has is
	// sent the state (see greetedClients), while every one of them counts
	// and is sent notices.
	clients map[*controlClient]bool

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

	// gitShown is the project whose checkouts the git loop was last asked to
	// read as the one on screen, and gitAllAt when every open project's were
	// last read for the overview of every pane. Both belong to the workspace
	// goroutine.
	gitShown string
	gitAllAt time.Time

	// loopDone is closed when the workspace goroutine returns. See Stopped.
	loopDone chan struct{}

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

	// unsavedAskedAt is when a quit or restart was last turned down because
	// the layout could not be saved. See askedAgainPastFailedSave. It is
	// touched only on the workspace goroutine.
	unsavedAskedAt time.Time

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

	// paneLookup, usageRefresh, pingInterval and pingTimeout are the package
	// variables of the same names, and saveInterval is layoutSaveInterval, read
	// once as the server is made. A test shortens them for the server it makes,
	// and a goroutine an earlier test's server left running would otherwise be
	// reading them while the next test writes them.
	paneLookup, usageRefresh, saveInterval time.Duration
	pingInterval, pingTimeout              time.Duration
	// conversations is allConversations, read once as the server is made, for
	// the same reason: a test replaces it on its own server, not for them all.
	conversations conversationSource

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

	// push is what the paired devices are told of waits. See push.go.
	push pushState

	// convos is the phone chat view's live streams, one per pane that has
	// been opened by at least one client. See conversation.go.
	convos conversationHub

	// preview is each agent pane's latest-reply preview, for the phone's
	// inbox row. See preview.go.
	preview previewCache
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
		links:   map[string]linkEntry{},
		spent:   map[string]linkFateEntry{},
		ln:      ln,
		clients: map[*controlClient]bool{},
		cmds:    make(chan func(), 64),
		dirty:   make(chan struct{}, 1),
		asked:   make(chan struct{}, 1),
		gitNow:  make(chan struct{}, 1),
		closed:  make(chan struct{}),

		linkLife:      windowLinkLife,
		loopDone:      make(chan struct{}),
		paneLookup:    paneLookup,
		usageRefresh:  usageRefresh,
		saveInterval:  layoutSaveInterval,
		pingInterval:  pingInterval,
		pingTimeout:   pingTimeout,
		conversations: allConversations,
		book:          spend.NewBook(),
		convos:        newConversationHub(),
		preview:       newPreviewCache(),
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
	mux.HandleFunc("/window", s.handleWindow)
	mux.HandleFunc("/remote/reload", s.handleRemoteReload)
	s.mux = mux

	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	go s.runLoop()
	go s.pushLoop()
	go s.gitLoop()
	go s.usageLoop()
	go s.saveLoop()
	go s.waitLoop()
	go s.conversationPollLoop()
	go cleanupAttachedImages(attachedImageMaxAge)
	s.installSpawnHandler()
	s.installContextHandler()
	s.installUsageHandler()

	return s, nil
}

// URL is the address of the interface, including the token that authorises
// it, for somebody to open by hand: it is what a run without a window prints.
// A window this program opens is given WindowURL instead.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?t=%s", s.ln.Addr().String(), s.token)
}

// linkParam is the query parameter a one-time link is carried in.
const linkParam = "w"

// windowLinkLife is how long a link from WindowURL can be used for. A variable
// so a test need not wait it out; a server reads it once, as it is made.
var windowLinkLife = time.Minute

// WindowURL is an address for a window this program opens: a link that can
// be used once, within windowLinkLife, and that the page exchanges for the
// token, in a cookie, as it loads.
//
// The window used to be opened at URL, and the address a browser is started
// with stays on its command line for as long as it runs -- all day, for the
// window -- where any other user of the machine can read it, in ps or
// /proc/<pid>/cmdline. With the token from it, a program of theirs could open
// the control socket and drive every agent: a WebSocket client that is not a
// browser sends no Origin to be turned away for. A link that has been used, or
// has run out, opens nothing.
//
// A link is no safer to leave on a command line than the token was --
// anybody quick enough to read it there could redeem it first -- so
// appwindow writes it into a private local file instead, and starts the
// browser at that. NewWindowLink is WindowURL for a caller in a position to
// have that file removed the moment the link stops being any good, rather
// than left for appwindow's own backstop timer to find; a second launch
// asking over /window (see handleWindow) has no such moment to hand it, and
// its file relies on that timer alone.
func (s *Server) WindowURL() string {
	url, _ := s.NewWindowLink()
	return url
}

// NewWindowLink is WindowURL, together with the link's own token -- not the
// full URL -- for SetLinkFile.
func (s *Server) NewWindowLink() (url, link string) {
	link = rand.Text()
	until := time.Now().Add(s.linkLife)
	s.linkMu.Lock()
	s.links[link] = linkEntry{until: until}
	s.linkMu.Unlock()
	time.AfterFunc(s.linkLife, func() { s.expireLink(link) })
	return fmt.Sprintf("http://%s/?%s=%s", s.ln.Addr().String(), linkParam, link), link
}

// SetLinkFile records that link's one-time link was written into a local
// redirect file at path (see appwindow), so that file is removed the moment
// the link is redeemed or runs out, instead of left for somebody to come
// across later. It reports whether link was still outstanding to record
// this against; where it was not -- redeemed or expired already, in the
// moment between the file being written and this being called -- the caller
// should remove path itself, since nothing will do it on its behalf.
func (s *Server) SetLinkFile(link, path string) bool {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	e, ok := s.links[link]
	if !ok {
		return false
	}
	e.file = path
	s.links[link] = e
	return true
}

// linkEntry is what a link from WindowURL is worth until it is redeemed or
// runs out: when that happens, and where it was written into a local
// redirect file, if anywhere (see SetLinkFile).
type linkEntry struct {
	until time.Time
	file  string
}

// redeemLink reports whether link is one WindowURL made that has been
// neither used nor run out, uses it up, and removes the file it was written
// into, if any.
func (s *Server) redeemLink(link string) bool {
	s.linkMu.Lock()
	e, ok := s.links[link]
	if ok {
		delete(s.links, link)
	}
	s.linkMu.Unlock()
	if !ok {
		return false
	}
	valid := time.Now().Before(e.until)
	if valid {
		s.recordFate(link, fateUsed)
	} else {
		s.recordFate(link, fateExpired)
	}
	if e.file != "" {
		_ = os.Remove(e.file)
	}
	return valid
}

// expireLink removes link once it has run out, for a link nobody ever loaded
// -- so its file, if it has one, is not left behind for as long as this
// instance keeps running. It does nothing where link has already been
// redeemed: that path has already recorded its fate and removed its file,
// and the two must not race each other over which.
func (s *Server) expireLink(link string) {
	s.linkMu.Lock()
	e, ok := s.links[link]
	if ok {
		delete(s.links, link)
	}
	s.linkMu.Unlock()
	if !ok {
		return
	}
	s.recordFate(link, fateExpired)
	if e.file != "" {
		_ = os.Remove(e.file)
	}
}

// clearLinkFiles removes the redirect file of every link still outstanding,
// as the server closes: a window never opened, or a second one asked for and
// never used, should not leave its file behind once nothing can use it
// either way.
func (s *Server) clearLinkFiles() {
	s.linkMu.Lock()
	links := s.links
	s.links = map[string]linkEntry{}
	s.linkMu.Unlock()
	for _, e := range links {
		if e.file != "" {
			_ = os.Remove(e.file)
		}
	}
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
			s.refreshShownGit()
		case <-s.gitNow:
			s.refreshShownGit()
		case <-tick.C:
			// Nothing is reading the branch labels while every window is
			// closed, and a detached run can sit like that for hours: polling
			// git over every checkout the whole time buys nobody anything.
			// A window that opens asks for a refresh of its own.
			if s.ClientCount() == 0 {
				continue
			}
			s.refreshShownGit()
		}
	}
}

// refreshShownGit refreshes the checkouts of the panes on screen: those on the
// tabs of the project being shown, which are the only pane headers a window
// draws. The panes of the other open projects are read when their project is
// brought on screen (see snapshot) and when the overview of every pane opens
// (see listAgents), which are the only places they are shown.
func (s *Server) refreshShownGit() {
	s.do(func() {
		var ids []string
		for _, t := range s.ws.VisibleTabs() {
			ids = append(ids, t.Tree.Panes()...)
		}
		go s.ws.RefreshGitOf(s.do, ids)
	})
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
// the app runs. A variable so a test need not wait it out; a server reads it
// once, as it is made, into Server.saveInterval.
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
	tick := time.NewTicker(s.saveInterval)
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
	s.tellKept()
	if err == nil {
		s.saveFailShown = false
		return
	}
	if !s.saveFailShown && s.ClientCount() > 0 {
		s.notifyAll("the layout could not be saved, and it is tried again every half minute; if this goes on, check that the disk has room and that Flockdeck's state folder can be written to: "+err.Error(), true)
		s.saveFailShown = true
	}
}

// tellKept tells the windows about every state file moved aside — one a save
// could not read, one that was damaged, or a layout a newer build saved — which
// is the first they hear of it: the project whose layout it was came up on one
// fresh tab at start, with nothing to say why, and the user's tabs were a file
// with a new name. Each is told once, and only once a window is there to be
// told; until then the store holds on to them.
func (s *Server) tellKept() {
	if s.ClientCount() == 0 {
		return
	}
	for _, k := range store.TakeKept() {
		s.notifyAll(k.Sentence(), true)
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

// handleIndex serves the page, promoting a token in the URL, or a one-time
// link from WindowURL, to a cookie holding the token, so that later requests
// from the page carry it automatically.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	link := r.URL.Query().Get(linkParam)
	linked := link != "" && s.redeemLink(link)
	if !linked && !s.authorised(r) {
		if link != "" {
			// A link was given and was no good: say why, in the house style,
			// rather than the bare refusal below -- which is for a load that
			// named no link at all, and is not worth explaining further.
			s.serveLinkGone(w, r, link)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Only a token that is actually ours is promoted. The request may have
	// been authorised by an existing cookie while carrying a stale `t` from a
	// bookmarked URL of an earlier run, and storing that would replace a
	// working cookie with one that no longer opens anything. A window reloaded
	// at a link it has already used is let in by its cookie in the same way.
	if linked || s.tokenMatches(r.URL.Query().Get("t")) {
		http.SetCookie(w, &http.Cookie{
			Name:     s.cookieName(),
			Value:    s.token,
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
	// Through the relay there is no cookie of this server's own to ride on --
	// the tunnel is what authorises a request there -- so nothing legitimate
	// frames this page at all, and the policy says so outright.
	csp := "frame-ancestors 'self'"
	if fromRemote(r) {
		csp = "frame-ancestors 'none'"
	}
	w.Header().Set("Content-Security-Policy", csp)
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
	s.clearLinkFiles()
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
