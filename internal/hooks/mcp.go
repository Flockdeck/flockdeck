package hooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/team"
)

// This file is the API behind the Flockdeck MCP server (`flockdeck mcp-server`).
// It differs from the team endpoints in how the caller is known: the server
// does not send a pane id. Each structured pane's MCP entry carries a token
// issued for that pane alone (IssuePaneToken), and the pane is whatever the
// token was issued for. The run's shared token does not work here and a pane's
// token does not work on any other endpoint.

const (
	// PathMCPSend and PathMCPPeers are the endpoints of the two MCP tools.
	PathMCPSend  = "/mcp/send"
	PathMCPPeers = "/mcp/peers"
	// PathMCPSpawn is the endpoint of the spawn tool.
	PathMCPSpawn = "/mcp/spawn"

	// maxMCPBody is the most a request body may hold: the largest message
	// escaped as JSON, with room for the field names.
	maxMCPBody = 6*team.MaxText + 1024

	// sendBurst messages may be sent at once by one pane, and one more is
	// allowed every sendEvery after that: 20 a minute for a pane that keeps going.
	sendBurst = 10
	sendEvery = 3 * time.Second
	// peersBurst and peersEvery are the same for list_peers.
	peersBurst = 30
	peersEvery = time.Second
	// spawnBurst helpers may be started at once by one pane, and one more is
	// allowed every spawnEvery: 6 a minute for a pane that keeps going. Each one
	// is a process and, by default, a worktree.
	spawnBurst = 3
	spawnEvery = 10 * time.Second
)

// mcpKind says which tool a request is for, and so which rate limit it counts against.
type mcpKind int

const (
	kindPeers mcpKind = iota
	kindSend
	kindSpawn
)

// MCPSendBody is the body of a send. It has no pane: unknown fields are refused,
// so a body that names a sender is refused rather than ignored.
type MCPSendBody struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

// MCPSpawnBody is the body of a spawn: the options of `flockdeck spawn` that make
// sense for an agent with no shell. It has no parent, token, shell, baton, task
// id or record: unknown fields are refused, and the parent is the pane of the
// bearer token.
type MCPSpawnBody struct {
	Task       string `json:"task"`
	Role       string `json:"role,omitempty"`
	Name       string `json:"name,omitempty"`
	Tab        string `json:"tab,omitempty"`
	Worktree   string `json:"worktree,omitempty"`
	NoWorktree bool   `json:"no_worktree,omitempty"`
	Split      bool   `json:"split,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Model      string `json:"model,omitempty"`
	Terminal   bool   `json:"terminal,omitempty"`
}

// MCPPeer is one pane of the caller's project as list_peers shows it. It has
// what a message can be addressed with and nothing else about the pane.
type MCPPeer struct {
	Pane   string `json:"pane"`
	Label  string `json:"label"`
	Role   string `json:"role,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Status string `json:"status"`
	Self   bool   `json:"self,omitempty"`
}

// MCPPeers is the answer to list_peers.
type MCPPeers struct {
	Peers []MCPPeer `json:"peers"`
}

// bucket is a token bucket: up to burst events at once, one more every every.
type bucket struct {
	tokens float64
	last   time.Time
}

// allow takes one event from b and says whether there was one to take, and if
// not how long until there is.
func (b *bucket) allow(now time.Time, burst int, every time.Duration) (bool, time.Duration) {
	if b.last.IsZero() {
		b.tokens = float64(burst)
	} else if d := now.Sub(b.last); d > 0 {
		b.tokens += float64(d) / float64(every)
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) * float64(every))
}

// paneTokens maps the hash of each issued token to its pane, and the pane to its
// token, so a pane has one at a time. Guarded by Server.mu.
type paneTokens struct {
	byHash map[[32]byte]string
	byPane map[string][32]byte
	send   map[string]*bucket
	peers  map[string]*bucket
	spawn  map[string]*bucket
}

// IssuePaneToken makes a token that acts as pane and no other, and drops the one
// the pane had before, so an agent process of an earlier session of the pane
// stops working. It returns "" if no token can be made.
func (s *Server) IssuePaneToken(pane string) string {
	if pane == "" {
		return ""
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	tok := hex.EncodeToString(raw)
	h := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	defer s.mu.Unlock()
	pt := &s.paneTok
	if pt.byHash == nil {
		pt.byHash, pt.byPane = map[[32]byte]string{}, map[string][32]byte{}
		pt.send, pt.peers, pt.spawn = map[string]*bucket{}, map[string]*bucket{}, map[string]*bucket{}
	}
	if old, ok := pt.byPane[pane]; ok {
		delete(pt.byHash, old)
	}
	pt.byHash[h], pt.byPane[pane] = pane, h
	return tok
}

// RevokePaneToken drops the token of a pane that is closed.
func (s *Server) RevokePaneToken(pane string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pt := &s.paneTok
	if h, ok := pt.byPane[pane]; ok {
		delete(pt.byHash, h)
		delete(pt.byPane, pane)
	}
	delete(pt.send, pane)
	delete(pt.peers, pane)
	delete(pt.spawn, pane)
}

// paneOf is the pane a token was issued for. Tokens are looked up by hash, so
// the comparison does not depend on how much of a guess was right.
func (s *Server) paneOf(token string) (string, bool) {
	h := sha256.Sum256([]byte(token))
	s.mu.RLock()
	defer s.mu.RUnlock()
	pane, ok := s.paneTok.byHash[h]
	return pane, ok
}

// limit takes one event from the pane's bucket of the given kind.
func (s *Server) limit(pane string, kind mcpKind) (bool, time.Duration) {
	now := time.Now()
	if s.mcpNow != nil {
		now = s.mcpNow()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, burst, every := s.paneTok.peers, peersBurst, peersEvery
	switch kind {
	case kindSend:
		m, burst, every = s.paneTok.send, sendBurst, sendEvery
	case kindSpawn:
		m, burst, every = s.paneTok.spawn, spawnBurst, spawnEvery
	}
	if m == nil {
		return false, every
	}
	b := m[pane]
	if b == nil {
		b = &bucket{}
		m[pane] = b
	}
	return b.allow(now, burst, every)
}

// mcpCaller checks the bearer token and the rate limit and returns the pane the
// request acts as. It has written the refusal when it returns false.
func (s *Server) mcpCaller(w http.ResponseWriter, r *http.Request, kind mcpKind) (string, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return "", false
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}
	pane, ok := s.paneOf(tok)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}
	// Every authenticated call counts, a refused one too: a loop of bad calls
	// is as much a flood as a loop of good ones.
	if ok, wait := s.limit(pane, kind); !ok {
		secs := int(wait.Seconds()) + 1
		w.Header().Set("Retry-After", fmt.Sprint(secs))
		http.Error(w, fmt.Sprintf("too many requests from this pane; wait %d seconds and try again", secs), http.StatusTooManyRequests)
		return "", false
	}
	return pane, true
}

// readStrict decodes one JSON object of at most maxMCPBody bytes into v and
// refuses a field v does not have.
func readStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMCPBody+1))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	if len(body) > maxMCPBody {
		http.Error(w, fmt.Sprintf("the request is larger than %d bytes", maxMCPBody), http.StatusRequestEntityTooLarge)
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		// The sender is fixed by the token. The reason is in the answer so a
		// model that tried to set it learns why it was refused.
		if strings.Contains(err.Error(), "unknown field") {
			http.Error(w, "bad request: "+err.Error()+"; the sender is the pane this server was started for and cannot be set", http.StatusBadRequest)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
		}
		return false
	}
	if dec.More() {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) handleMCPSend(w http.ResponseWriter, r *http.Request) {
	pane, ok := s.mcpCaller(w, r, kindSend)
	if !ok {
		return
	}
	var body MCPSendBody
	if !readStrict(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.To) == "" || body.Text == "" {
		http.Error(w, "to and text are both required", http.StatusBadRequest)
		return
	}
	if len(body.Text) > team.MaxText {
		http.Error(w, fmt.Sprintf("the message is %d bytes and the limit is %d; write it to a file and send the path instead", len(body.Text), team.MaxText), http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	fn := s.onTeam.msg
	s.mu.RUnlock()
	if fn == nil {
		http.Error(w, "messaging is not available", http.StatusServiceUnavailable)
		return
	}
	res, err := fn(MsgRequest{Auth: Auth{Pane: pane}, Target: body.To, Text: body.Text})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleMCPPeers(w http.ResponseWriter, r *http.Request) {
	pane, ok := s.mcpCaller(w, r, kindPeers)
	if !ok {
		return
	}
	var none struct{}
	if !readStrict(w, r, &none) {
		return
	}
	s.mu.RLock()
	fn := s.onTeam.team
	s.mu.RUnlock()
	if fn == nil {
		http.Error(w, "the team list is not available", http.StatusServiceUnavailable)
		return
	}
	res, err := fn(TeamRequest{Auth{Pane: pane}})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out := MCPPeers{Peers: []MCPPeer{}}
	for _, m := range res.Members {
		out.Peers = append(out.Peers, MCPPeer{Pane: m.Pane, Label: m.Label(), Role: m.Role, Agent: m.Agent, Status: m.Status, Self: m.Self})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleMCPSpawn starts a helper for the pane of the token. It builds the same
// SpawnRequest `flockdeck spawn` posts and gives it to the same handler, so the
// checks of the role the caller is in, the room for helpers, the agent and the
// role asked for, the worktree default and the routing of the helper to a
// project all apply as they do to the command.
func (s *Server) handleMCPSpawn(w http.ResponseWriter, r *http.Request) {
	pane, ok := s.mcpCaller(w, r, kindSpawn)
	if !ok {
		return
	}
	var body MCPSpawnBody
	if !readStrict(w, r, &body) {
		return
	}
	req, err := body.request(pane)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	fn := s.onSpawn
	s.mu.RUnlock()
	if fn == nil {
		http.Error(w, "spawning is not available", http.StatusServiceUnavailable)
		return
	}
	req.Ctx = r.Context()
	res, err := fn(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// request checks the body as `flockdeck spawn` checks its flags and returns the
// request for pane to make.
func (b MCPSpawnBody) request(pane string) (SpawnRequest, error) {
	task := strings.TrimSpace(b.Task)
	if task == "" {
		return SpawnRequest{}, errors.New("a task is required")
	}
	if b.NoWorktree && b.Worktree != "" {
		return SpawnRequest{}, errors.New("worktree asks for a worktree and no_worktree for none, so they cannot go together")
	}
	tab, err := CleanTabName(b.Tab)
	if err != nil {
		return SpawnRequest{}, errors.New(strings.Replace(err.Error(), "-tab", "tab", 1))
	}
	return SpawnRequest{
		Parent:     pane,
		Task:       task,
		Branch:     strings.TrimSpace(b.Worktree),
		NoWorktree: b.NoWorktree,
		Split:      b.Split,
		Tab:        tab,
		Label:      team.PaneLabelText(b.Name),
		Agent:      b.Agent,
		Model:      b.Model,
		Terminal:   b.Terminal,
		Role:       b.Role,
	}, nil
}

// postBearer is post with the token in a header and nothing of it in the body.
func postBearer[Res any](ctx context.Context, api, path, token string, wait time.Duration, req any) (Res, error) {
	var none Res
	body, err := json.Marshal(req)
	if err != nil {
		return none, err
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, api+path, bytes.NewReader(body))
	if err != nil {
		return none, errors.New("the Flockdeck address is not usable")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		// The error of net/http names the URL and nothing of the token, but the
		// answer to a model is fixed text all the same.
		if errors.Is(err, context.DeadlineExceeded) {
			if path == PathMCPSpawn {
				return none, fmt.Errorf("Flockdeck did not answer within %s; the helper may still be starting, so call list_peers before asking again", wait)
			}
			return none, fmt.Errorf("Flockdeck did not answer within %s; try again shortly", wait)
		}
		return none, errors.New("Flockdeck is not answering; it may have been closed since this pane started")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if text := strings.TrimSpace(string(msg)); text != "" {
			return none, errors.New(text)
		}
		return none, fmt.Errorf("Flockdeck refused: %s", resp.Status)
	}
	var out Res
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return none, errors.New("unreadable answer from Flockdeck")
	}
	return out, nil
}

// MCPSend is the client half of the send_message tool.
func MCPSend(ctx context.Context, api, token, to, text string) (MsgResult, error) {
	return postBearer[MsgResult](ctx, api, PathMCPSend, token, teamTimeout, MCPSendBody{To: to, Text: text})
}

// MCPListPeers is the client half of the list_peers tool.
func MCPListPeers(ctx context.Context, api, token string) (MCPPeers, error) {
	return postBearer[MCPPeers](ctx, api, PathMCPPeers, token, teamTimeout, struct{}{})
}

// MCPSpawn is the client half of the spawn tool. It waits as long as `flockdeck
// spawn` does for a helper that needs no say-so from the user.
func MCPSpawn(ctx context.Context, api, token string, body MCPSpawnBody) (SpawnResult, error) {
	res, err := postBearer[SpawnResult](ctx, api, PathMCPSpawn, token, spawnTimeout, body)
	if err == nil && res.PaneID == "" {
		return res, errors.New("Flockdeck accepted the request but named no pane")
	}
	return res, err
}
