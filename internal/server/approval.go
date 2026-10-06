package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/hooks"
)

// What the approval is, and what it is not.
//
// An agent that passes -baton-send-elsewhere has only asked. The user is shown a
// notice with a button in a window, and the baton is sent when the button is
// pressed. That stops an agent that makes a mistake, and a request nobody saw. It
// does not stop a hostile process running as the same user: such a process can
// read the window token from the instance file in the state directory, open the
// control socket with an Origin header of its own making, receive the notice and
// press the button. What is done to make that harder, and no more than harder:
//
//   - the notice goes only to a connection that opened with an Origin header, which
//     a browser or webview always sends and the agent's own commands do not;
//   - every window gets a token of its own, and a token is accepted only from the
//     connection it was sent to, once;
//   - a window reached through the relay is never asked.

// batonApprovalWait is how long a spawn that would send a baton to another company
// waits for the user to allow it in a window. It is under hooks' spawn timeout, so
// the agent is told the answer and not that Flockdeck did not reply.
var batonApprovalWait = hooks.BatonApprovalWait

// pendingApprovals are the requests waiting for a user, by token.
var pendingApprovals = struct {
	sync.Mutex
	byToken map[string]*approval
}{byToken: map[string]*approval{}}

// approval is one request: the window each token was sent to, and the channel its
// answer comes on.
type approval struct {
	id      string
	windows map[string]*controlClient // token to the connection it was sent to
	done    chan *controlClient       // the window that allowed it
	ack     chan bool                 // whether the spawn took the approval

	// state is open until a window claims the approval, and closed once the request
	// has ended without one. The claim and the end are decided under mu, so a
	// request that ended is never reported as sent.
	mu    sync.Mutex
	state int
}

// noticeAction is the one button a notice may carry: its label, and the command
// the window sends back when it is pressed.
type noticeAction struct {
	Label string            `json:"label"`
	Send  map[string]string `json:"send"`
}

// approvalNotice is a notice that asks for an answer: it names what is being
// asked, stays for as long as the request does, and is withdrawn when the request
// ends. ID is what a window withdraws it by.
type approvalNotice struct {
	Type   string        `json:"type"`
	Text   string        `json:"text"`
	Error  bool          `json:"error"`
	Lead   string        `json:"lead"`
	Action *noticeAction `json:"action"`
	LifeMS int           `json:"lifeMs"`
	Pin    bool          `json:"pin"`
	ID     string        `json:"id"`
	Kind   string        `json:"kind"`
}

// approvalInfo says who asked and where the baton would go, for the notice.
type approvalInfo struct {
	Asker  string // the pane that asked, and its agent
	Dest   string // the agent the helper would run and its company
	Source string // where the baton came from, or that it is not known
	// Gist is how big the baton is and how it begins, so that what is being
	// approved is something the user can see. Empty when there is no baton text.
	Gist string
}

// batonGist says how big a baton is and how it begins: its size, and the first
// line of its goal as plain text, cut short. The text is already scrubbed; it is
// cleaned of control and hidden characters here as well, since it is shown in a
// notice a person decides on. A task that goes with it is counted too, by its size.
func batonGist(b baton.Baton, task string) string {
	size := len(baton.Render(b))
	// The goal, or for a notes file the notes, or whatever section has text first.
	first := ""
	for _, sec := range append([]baton.Section{baton.Goal, baton.Standing}, baton.Sections...) {
		for _, line := range strings.Split(baton.CleanText(b.Section(sec)), "\n") {
			if line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "#>*-` ")); line != "" && line != baton.Placeholder {
				first = line
				break
			}
		}
		if first != "" {
			break
		}
	}
	if first == "" {
		first = strings.TrimSpace(baton.CleanText(b.Title))
	}
	if r := []rune(first); len(r) > 100 {
		first = string(r[:100]) + "..."
	}
	text := "It is " + humanSize(size)
	if first != "" {
		text += " and begins: \"" + first + "\""
	}
	if task = strings.TrimSpace(task); task != "" {
		text += ", plus a task of " + humanSize(len(task))
	}
	return text
}

// humanSize is a size in bytes as a person reads it.
func humanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d bytes", n)
	case n < 10<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// gistSentence is the gist as a sentence of the notice, or nothing.
func gistSentence(g string) string {
	if g == "" {
		return ""
	}
	return g + ". "
}

func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// approveElsewhere asks the user, in each window on this machine that opened with
// an Origin, to allow a baton to go to another company, and waits for one to say
// yes. No such window, no answer in time, a Flockdeck that is closing, or the
// agent's own request being cancelled is a refusal, and the notices are withdrawn.
func (s *Server) approveElsewhere(ctx context.Context, info approvalInfo) error {
	var windows []*controlClient
	for _, c := range s.clientList() {
		if c.page && !c.remote {
			windows = append(windows, c)
		}
	}
	if len(windows) == 0 {
		return errors.New("the baton was not sent: no Flockdeck window is open on this machine to ask whether it may be sent to another provider")
	}
	id, err := randomHex(8)
	if err != nil {
		return errors.New("the baton was not sent: Flockdeck could not make a request for the user to answer")
	}
	a := &approval{id: id, windows: map[string]*controlClient{}, done: make(chan *controlClient, 1), ack: make(chan bool, 1)}
	notices := map[*controlClient][]byte{}
	for _, c := range windows {
		token, err := randomHex(16)
		if err != nil {
			return errors.New("the baton was not sent: Flockdeck could not make a request for the user to answer")
		}
		a.windows[token] = c
		data, err := json.Marshal(approvalNotice{
			Type: "notice",
			Text: info.Asker + " asked to start " + info.Dest + " from a baton that " + info.Source +
				". " + gistSentence(info.Gist) + "It holds what an agent worked out. Allow it only if you asked for this.",
			Lead:   "Baton leaving its provider.",
			Action: &noticeAction{Label: "Send it", Send: map[string]string{"cmd": "approveBaton", "text": token}},
			LifeMS: int(batonApprovalWait / time.Millisecond),
			Pin:    true,
			ID:     id,
			Kind:   "approval",
		})
		if err != nil {
			return err
		}
		notices[c] = data
	}
	pendingApprovals.Lock()
	for token := range a.windows {
		pendingApprovals.byToken[token] = a
	}
	pendingApprovals.Unlock()
	withdraw := func(except *controlClient) {
		pendingApprovals.Lock()
		for token := range a.windows {
			delete(pendingApprovals.byToken, token)
		}
		pendingApprovals.Unlock()
		gone, _ := json.Marshal(map[string]string{"type": "noticeWithdraw", "id": id})
		for _, c := range windows {
			c.send(gone)
			if c != except && except != nil {
				c.notifyApproval("Another window allowed that baton.", false)
			}
		}
	}
	for c, data := range notices {
		c.send(data)
	}
	timer := time.NewTimer(batonApprovalWait)
	defer timer.Stop()
	// The request ends one way: allowed, or not. A window that claimed it as the
	// request was ending is told it ran out, and the spawn is not started.
	ended := func(err error) error {
		a.mu.Lock()
		claimed := a.state == approvalClaimed
		a.state = approvalEnded
		a.mu.Unlock()
		if claimed {
			<-a.done // the claim is on its way
			a.ack <- false
		}
		withdraw(nil)
		return err
	}
	select {
	case by := <-a.done:
		// An approval that arrives after the request was given up on is not taken.
		if ctx.Err() != nil {
			a.ack <- false
			withdraw(nil)
			return errors.New("the baton was not sent: the request was cancelled before it was allowed")
		}
		a.ack <- true
		withdraw(by)
		return nil
	case <-timer.C:
		return ended(errors.New("the baton was not sent: it was not allowed in the Flockdeck window in time"))
	case <-ctx.Done():
		return ended(errors.New("the baton was not sent: the request was cancelled before it was allowed"))
	case <-s.closed:
		return ended(errShuttingDown)
	}
}

const (
	approvalOpen = iota
	approvalClaimed
	approvalEnded
)

// approveClaimed is called in the gap between a window claiming an approval and the
// spawn being told, so a test can end the request there.
var approveClaimed = func() {}

// approveBaton is a window allowing the request its notice carried. Only the
// connection the token was sent to can use it, and only once. One reached through
// the relay, or one that did not open with an Origin, is refused.
func (s *Server) approveBaton(c *controlClient, token string) {
	if c.remote || !c.page {
		c.notifyApproval("A baton can only be sent to another provider from the Flockdeck window on this machine.", true)
		return
	}
	pendingApprovals.Lock()
	a, ok := pendingApprovals.byToken[token]
	to := a != nil && a.windows[token] == c
	if ok && to {
		for t := range a.windows {
			delete(pendingApprovals.byToken, t)
		}
	}
	pendingApprovals.Unlock()
	switch {
	case !ok:
		c.notifyApproval("That request has run out. Ask the agent to try again.", true)
	case !to:
		c.notifyApproval("That request was sent to another window, or to this one before it reconnected. Ask the agent to try again.", true)
	default:
		a.mu.Lock()
		if a.state != approvalOpen {
			a.mu.Unlock()
			c.notifyApproval("That request has run out. Ask the agent to try again.", true)
			return
		}
		a.state = approvalClaimed
		a.mu.Unlock()
		approveClaimed()
		a.done <- c
		// Said only once the spawn has taken it.
		if <-a.ack {
			c.notifyApproval("Approved. Sending the baton.", false)
		} else {
			c.notifyApproval("That request has run out. Ask the agent to try again.", true)
		}
	}
}

// notifyApproval is notify for what is said of a request for the user's answer. It is
// marked, so the window does not take it for the answer to something else it waits on.
func (c *controlClient) notifyApproval(text string, isErr bool) {
	if data, err := json.Marshal(noticeMsg{Type: "notice", Text: text, Error: isErr, Kind: "approval"}); err == nil {
		c.send(data)
	}
}
