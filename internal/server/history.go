package server

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// conversationView is one stored conversation as the history panel shows it.
type conversationView struct {
	ID string `json:"id"`
	// Agent is the id of the agent that held the conversation. The panel lists
	// every agent's conversations in one place, and two rows can otherwise say
	// nothing that tells them apart -- the same project, the same prompt, one
	// held by Claude Code and one by a model spoken to directly.
	Agent    string `json:"agent,omitempty"`
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

// listings remembers the newest conversation listing each window has asked
// for.
//
// Reading a project's transcripts takes as long as the project is old, so two
// answers can finish out of order: open the history of a project with years
// behind it, close it, open a younger project's, and the first answer lands
// on top of the second. The panel redraws itself from whichever message
// arrived last, so the window would be left looking at another project's
// conversations under this project's heading. Only the newest request a
// window has made is allowed to answer it.
var listings = struct {
	sync.Mutex
	seq map[*controlClient]uint64
}{seq: make(map[*controlClient]uint64)}

// askedForListing records that a window has asked, and numbers the request.
func askedForListing(c *controlClient) uint64 {
	listings.Lock()
	defer listings.Unlock()
	listings.seq[c]++
	return listings.seq[c]
}

// answerListing reports whether a finished listing is still the one its
// window is waiting for, and forgets the window when it is.
func answerListing(c *controlClient, n uint64) bool {
	listings.Lock()
	defer listings.Unlock()
	if listings.seq[c] != n {
		return false
	}
	delete(listings.seq, c)
	return true
}

// allConversations reads every agent's stored conversations. It is a variable
// so a test can have one agent's store fail beside another's that works, which
// no file system arrangement does reliably everywhere.
var allConversations = transcript.All

// listConversations answers a window's request for the project's conversation
// history. Reading transcripts touches the disk, so it happens away from the
// goroutine that owns the workspace.
func (s *Server) listConversations(c *controlClient, cwd string) {
	asked := askedForListing(c)
	dir, ok := ask(s, func() string {
		if cwd == "" {
			return s.ws.ActiveRoot()
		}
		return cwd
	})
	if !ok {
		// Nothing will answer the request now, so it is forgotten rather than
		// left in the listings for good.
		answerListing(c, asked)
		return
	}

	go func() {
		defer s.survive("listing conversations")
		msg := conversationsMsg{Type: "conversations", Cwd: dir}
		items, err := allConversations(transcript.Agents(), dir)
		// One agent's store being unreadable is worth saying, but not at the
		// price of what the others found. Only a listing with nothing in it
		// carries the error: the panel draws an error in place of the whole
		// list, so one sent alongside conversations hid every one of them.
		// With conversations to show it goes beside them, as a notice.
		if err != nil && len(items) == 0 {
			msg.Error = err.Error()
			if answerListing(c, asked) {
				c.sendJSON(msg)
			}
			return
		}
		open := s.openConversationIDs()
		for _, conv := range items {
			msg.Items = append(msg.Items, conversationView{
				ID:       conv.ID,
				Agent:    conv.Agent,
				Summary:  conv.Summary,
				Modified: conv.Modified.Format("2006-01-02 15:04"),
				Ago:      humanAgo(time.Since(conv.Modified)),
				Messages: conv.Messages,
				KB:       int(conv.Size / 1024),
				Open:     open[conv.ID],
			})
		}
		if answerListing(c, asked) {
			c.sendJSON(msg)
			if err != nil {
				c.notify(err.Error(), true)
			}
		}
	}()
}

// openConversationIDs reports which conversations already have a pane. On a
// workspace that has stopped it reports none, which is what a list nobody
// will see needs it to be.
func (s *Server) openConversationIDs() map[string]bool {
	ids, _ := ask(s, func() map[string]bool {
		ids := map[string]bool{}
		for _, t := range s.ws.Tabs {
			for _, id := range t.Tree.Panes() {
				ids[id] = true
			}
		}
		return ids
	})
	return ids
}

// resumeConversation opens a stored conversation in a new tab.
func (s *Server) resumeConversation(c *controlClient, id, cwd, title string) {
	s.do(func() {
		if err := s.ws.OpenConversation(id, cwd, title); err != nil {
			c.notify(err.Error(), true)
			return
		}
		s.wakeAsked()
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
	if summary == session.NoPrompt {
		// The stand-in the history panel shows for a conversation that says
		// nothing about itself. It reads as a row in a list; as the name of a
		// pane it says even less than the directory does.
		summary = ""
	}
	words := []rune(summary)
	if len(words) == 0 {
		return filepath.Base(cwd)
	}
	if len(words) > 24 {
		return string(words[:24]) + "…"
	}
	return string(words)
}
