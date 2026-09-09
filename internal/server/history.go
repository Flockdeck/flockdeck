package server

import (
	"path/filepath"
	"time"

	"github.com/jmwri/perch/internal/session"
)

// conversationView is one stored conversation as the history panel shows it.
type conversationView struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Modified string `json:"modified"`
	Ago      string `json:"ago"`
	Messages int    `json:"messages"`
	KB       int    `json:"kb"`
	Open     bool   `json:"open"`
}

type conversationsMsg struct {
	Type  string             `json:"type"`
	Cwd   string             `json:"cwd"`
	Items []conversationView `json:"items"`
	Error string             `json:"error,omitempty"`
}

// listConversations answers a window's request for the project's conversation
// history. Reading transcripts touches the disk, so it happens away from the
// goroutine that owns the workspace.
func (s *Server) listConversations(c *controlClient, cwd string) {
	done := make(chan string, 1)
	s.do(func() {
		if cwd == "" {
			cwd = s.ws.ActiveRoot()
		}
		done <- cwd
	})
	// A request handed to a workspace that has already stopped is never run,
	// so waiting on its answer waits for good. This runs on the goroutine
	// that reads the window's socket, and that goroutine wedged is a window
	// that cannot be closed and a shutdown that does not finish.
	var dir string
	select {
	case dir = <-done:
	case <-s.closed:
		return
	}

	go func() {
		msg := conversationsMsg{Type: "conversations", Cwd: dir}
		items, err := session.Conversations(dir)
		if err != nil {
			msg.Error = err.Error()
			c.sendJSON(msg)
			return
		}
		open := s.openConversationIDs()
		for _, conv := range items {
			msg.Items = append(msg.Items, conversationView{
				ID:       conv.ID,
				Summary:  conv.Summary,
				Modified: conv.Modified.Format("2006-01-02 15:04"),
				Ago:      humanAgo(time.Since(conv.Modified)),
				Messages: conv.Messages,
				KB:       int(conv.Size / 1024),
				Open:     open[conv.ID],
			})
		}
		c.sendJSON(msg)
	}()
}

// openConversationIDs reports which conversations already have a pane. On a
// workspace that has stopped it reports none, which is what a list nobody
// will see needs it to be.
func (s *Server) openConversationIDs() map[string]bool {
	done := make(chan map[string]bool, 1)
	s.do(func() {
		ids := map[string]bool{}
		for _, t := range s.ws.Tabs {
			for _, id := range t.Tree.Panes() {
				ids[id] = true
			}
		}
		done <- ids
	})
	select {
	case ids := <-done:
		return ids
	case <-s.closed:
		return nil
	}
}

// resumeConversation opens a stored conversation in a new tab.
func (s *Server) resumeConversation(c *controlClient, id, cwd, title string) {
	s.do(func() {
		if err := s.ws.OpenConversation(id, cwd, title); err != nil {
			c.notify(err.Error(), true)
			return
		}
		s.Wake()
	})
}

// humanAgo renders a duration the way a list of recent things wants it.
func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	default:
		return plural(int(d.Hours()/24/30), "month") + " ago"
	}
}

func plural(n int, unit string) string {
	if n <= 1 {
		return "1 " + unit
	}
	return itoa(n) + " " + unit + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// titleFor names a resumed tab after its opening prompt, which is far more
// useful in a tab bar than a session id.
func titleFor(summary, cwd string) string {
	words := []rune(summary)
	if len(words) == 0 {
		return filepath.Base(cwd)
	}
	if len(words) > 24 {
		return string(words[:24]) + "…"
	}
	return string(words)
}
