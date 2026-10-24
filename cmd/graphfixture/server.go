package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// A reader of the page's files is told the same things the real server tells
// it: nothing is cached, and nothing frames the page.
const cookieName = "graphfixture"

// The desk's limit on a paneSend text and its words for the two texts it refuses
// before anything else. They are copied here, and not read from
// internal/workspace, so that the tool links nothing that could look for the
// configuration directory; a test pins them to workspace.
const (
	maxPaneSendBytes = 16 * 1024
	sendTooLongWords = "That text is over 16 KB, so it was not sent"
	sendEmptyWords   = "There was no text to send"
)

// sentText is one paneSend the fake socket took.
type sentText struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// fixtureServer serves a front end and plays one fixture on a fake control
// socket. It starts no workspace and reads no configuration: the key table is
// the compiled-in one, and every other answer is made here.
type fixtureServer struct {
	fx    *fixture
	files fs.FS
	token string

	mu   sync.Mutex
	sent []sentText
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// listen binds loopback on a port the system chooses.
func listen() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

func (s *fixtureServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.Handle("/assets/", http.StripPrefix("/assets/", s.guarded(http.FileServer(http.FS(s.files)))))
	mux.HandleFunc("/ws/control", s.control)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	return s.loopbackOnly(mux)
}

// loopbackOnly refuses a request whose Host is not this machine's loopback, so
// a page on another name cannot reach the tool by rebinding its own.
func (s *fixtureServer) loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *fixtureServer) tokenMatches(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *fixtureServer) authorised(r *http.Request) bool {
	if c, err := r.Cookie(cookieName); err == nil && s.tokenMatches(c.Value) {
		return true
	}
	return s.tokenMatches(r.URL.Query().Get("t"))
}

func (s *fixtureServer) guarded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *fixtureServer) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.tokenMatches(r.URL.Query().Get("t")) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	data, err := fs.ReadFile(s.files, "index.html")
	if err != nil {
		http.Error(w, "missing front end", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	_, _ = w.Write(data)
}

// Sent is every text the fake socket was sent with paneSend and took, in order.
func (s *fixtureServer) Sent() []sentText {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentText(nil), s.sent...)
}

// control is the fake /ws/control. A window is greeted with the hello and then
// the state, and its commands are answered as the desk would for this fixture.
func (s *fixtureServer) control(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	ctx := r.Context()
	put := func(v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return false
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, data) == nil
	}
	if !put(helloMsg()) || !put(s.fx.stateMsg()) {
		return
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var cmd struct {
			Cmd  string `json:"cmd"`
			Req  string `json:"req"`
			Root string `json:"root"`
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		if json.Unmarshal(data, &cmd) != nil {
			continue
		}
		for _, reply := range s.answer(cmd.Cmd, cmd.Req, cmd.Root, cmd.ID, cmd.Text) {
			if !put(reply) {
				return
			}
		}
	}
}

// answer is what the fake desk says to one command. Commands it has no answer
// for are dropped, as the desk drops one it does not know.
func (s *fixtureServer) answer(cmd, req, root, id, text string) []any {
	switch cmd {
	case "graphWatch", "graphSnapshot":
		if root != "" && !sameRoot(root, s.fx.root) {
			return []any{map[string]any{"type": "teamResult", "req": req, "op": cmd, "ok": false, "error": "no project is open at " + root}}
		}
		return []any{json.RawMessage(s.fx.raw)}
	case "teamWatch":
		if s.fx.team == nil {
			return nil
		}
		return []any{json.RawMessage(s.fx.team)}
	case "paneSend":
		return s.paneSend(id, text)
	case "conversationOpen", "conversationOlder":
		if !s.fx.has(id) {
			return nil
		}
		return []any{conversationPage(id)}
	}
	return nil
}

func sameRoot(a, b string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.TrimRight(strings.ReplaceAll(s, "\\", "/"), "/"))
	}
	return norm(a) == norm(b)
}

// paneSend takes a text for a pane of the fixture, with the desk's words for the
// two texts it refuses first and for a pane it does not have. The text is kept
// as sent, so a test can read what reached the socket.
func (s *fixtureServer) paneSend(id, text string) []any {
	notice := func(msg string) []any {
		return []any{map[string]any{"type": "notice", "text": msg, "error": true}}
	}
	if len(text) > maxPaneSendBytes {
		return notice(sendTooLongWords)
	}
	if strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\r' && r != '\n' || r >= 0x7f && r <= 0x9f {
			return -1
		}
		return r
	}, text)) == "" {
		return notice(sendEmptyWords)
	}
	if !s.fx.has(id) {
		return notice("That pane is no longer open")
	}
	s.mu.Lock()
	s.sent = append(s.sent, sentText{ID: id, Text: text})
	s.mu.Unlock()
	return []any{map[string]any{"type": "paneSend", "id": id, "queued": false, "shell": false}}
}

// conversationPage is a canned page of two entries, the newest the whole of it.
func conversationPage(id string) map[string]any {
	return map[string]any{
		"type": "conversationPage", "id": id, "supported": true, "agent": "claude",
		"entries": []any{
			map[string]any{"id": "e1", "kind": "prompt", "ts": "2026-10-21T08:00:00Z", "text": "Start on the task."},
			map[string]any{"id": "e2", "kind": "reply", "ts": "2026-10-21T08:00:30Z", "markdown": "Working on it."},
		},
		"cursor": "e2", "atStart": true, "reset": true,
	}
}
