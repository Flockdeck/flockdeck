package team

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// MaxText is the most a message may carry, in bytes. A message is for a
	// sentence or a paragraph: anything longer is a document, which belongs in a
	// file the receiver reads with its own tools.
	MaxText = 8 * 1024
	// MaxInbox is how many messages one pane's inbox holds.
	MaxInbox = 100
	// KeepRead is how long a message that has been read is kept across a restart.
	KeepRead = 7 * 24 * time.Hour
	// KeepUnread is how long a message that was never read is kept across a
	// restart. It bounds what an inbox of a pane that no longer exists leaves
	// behind.
	KeepUnread = 30 * 24 * time.Hour
)

// Store holds every pane's inbox. It is safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	next  int
	boxes map[string][]Message
	now   func() time.Time
	// changed is told, outside the lock, after every change a restart should
	// keep.
	changed func()
}

// NewStore returns an empty store that reads the time from now (time.Now when
// nil).
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{boxes: map[string][]Message{}, now: now, next: 1}
}

// OnChange installs the function called after each change worth saving. It must
// not call back into the store.
func (s *Store) OnChange(fn func()) {
	s.mu.Lock()
	s.changed = fn
	s.mu.Unlock()
}

func (s *Store) notify() {
	s.mu.Lock()
	fn := s.changed
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func tooLong(n int) error {
	return fmt.Errorf("the message is %d bytes and the limit is %d; write it to a file and send the path instead", n, MaxText)
}

// Send puts a message in the inbox of pane to. A full inbox drops its oldest
// message that has been read; if every message in it is unread the send is
// refused, so nothing unread is lost.
func (s *Store) Send(from Sender, to, text string) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Message{}, fmt.Errorf("the message is empty")
	}
	if len(text) > MaxText {
		return Message{}, tooLong(len(text))
	}
	s.mu.Lock()
	box := s.boxes[to]
	if len(box) >= MaxInbox {
		i := firstRead(box)
		if i < 0 {
			s.mu.Unlock()
			return Message{}, fmt.Errorf("the inbox of that pane is full with %d unread messages; it has to read some first", len(box))
		}
		box = append(box[:i:i], box[i+1:]...)
	}
	m := Message{ID: s.next, From: from, To: to, Time: s.now(), Text: text}
	s.next++
	s.boxes[to] = append(box, m)
	s.mu.Unlock()
	s.notify()
	return m, nil
}

// SendSystem is Send for a message Flockdeck writes. When the inbox is full of
// unread messages the new one is dropped, since it only repeats what the roster
// already shows.
func (s *Store) SendSystem(to, text string) (Message, bool) {
	m, err := s.Send(Sender{Name: System}, to, text)
	return m, err == nil
}

func firstRead(box []Message) int {
	for i, m := range box {
		if m.Read {
			return i
		}
	}
	return -1
}

// List returns a pane's messages, oldest first: the unread ones, or all of them.
func (s *Store) List(pane string, all bool) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Message
	for _, m := range s.boxes[pane] {
		if all || !m.Read {
			out = append(out, m)
		}
	}
	return out
}

// Messages returns every message in the inboxes of the given panes, oldest
// first by id, as copies. It is how the window lists a project's messages; an
// agent reads only its own inbox, with List.
func (s *Store) Messages(panes []string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Message
	for _, p := range panes {
		out = append(out, s.boxes[p]...)
	}
	slices.SortFunc(out, func(a, b Message) int { return a.ID - b.ID })
	return out
}

// Read marks one of a pane's messages read and returns it. A pane can only read
// what is in its own inbox: an id that is in another's is not found.
func (s *Store) Read(pane string, id int) (Message, error) {
	s.mu.Lock()
	box := s.boxes[pane]
	for i := range box {
		if box[i].ID != id {
			continue
		}
		changed := !box[i].Read
		box[i].Read = true
		m := box[i]
		s.mu.Unlock()
		if changed {
			s.notify()
		}
		return m, nil
	}
	s.mu.Unlock()
	return Message{}, fmt.Errorf("no message #%d in your inbox; `flockdeck inbox -all` lists what is there", id)
}

// Forget drops a pane's inbox, as when the pane is closed.
func (s *Store) Forget(pane string) {
	s.mu.Lock()
	_, had := s.boxes[pane]
	delete(s.boxes, pane)
	s.mu.Unlock()
	if had {
		s.notify()
	}
}

type snapshot struct {
	Version int                  `json:"version"`
	Next    int                  `json:"next"`
	Boxes   map[string][]Message `json:"boxes"`
}

// Snapshot is the store as JSON, for saving.
func (s *Store) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	boxes := make(map[string][]Message, len(s.boxes))
	for pane, box := range s.boxes {
		if len(box) > 0 {
			boxes[pane] = box
		}
	}
	return json.MarshalIndent(snapshot{Version: 1, Next: s.next, Boxes: boxes}, "", "  ")
}

// Load replaces the store's contents with what Snapshot wrote. Messages that
// were read more than KeepRead ago, that were sent more than KeepUnread ago,
// and any that break the limits, are dropped.
func (s *Store) Load(data []byte) error {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	cutoff := s.now().Add(-KeepRead)
	oldest := s.now().Add(-KeepUnread)
	boxes := map[string][]Message{}
	next := max(snap.Next, 1)
	for pane, box := range snap.Boxes {
		var kept []Message
		for _, m := range box {
			if m.ID <= 0 || m.To != pane || len(m.Text) > MaxText || (m.Read && m.Time.Before(cutoff)) || m.Time.Before(oldest) {
				continue
			}
			next = max(next, m.ID+1)
			kept = append(kept, m)
		}
		if len(kept) > MaxInbox {
			kept = kept[len(kept)-MaxInbox:]
		}
		if len(kept) > 0 {
			boxes[pane] = kept
		}
	}
	s.mu.Lock()
	s.boxes, s.next = boxes, next
	s.mu.Unlock()
	return nil
}

// NewestUnread is the id of a pane's newest unread message, or zero when it has
// none.
func (s *Store) NewestUnread(pane string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	box := s.boxes[pane]
	for i := len(box) - 1; i >= 0; i-- {
		if !box[i].Read {
			return box[i].ID
		}
	}
	return 0
}
