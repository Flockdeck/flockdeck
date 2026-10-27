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
	// rev counts the changes, for a reader that wants to know if any happened.
	rev uint64
	// event is told, outside the lock, of each message sent and each delivery
	// state reached.
	event func(Event)
	// router names the route of a message to a pane (see SetRouter).
	router func(pane string) string
}

// NewStore returns an empty store that reads the time from now (time.Now when
// nil).
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{boxes: map[string][]Message{}, now: now, next: 1}
}

// SetRouter installs the function that says how mail to a pane is delivered: it
// returns RoutePrompt for a pane that is sent its messages as prompts, and "" for
// any other. A message made by Send takes the route its receiver has at that
// moment, so every sender, a system notice included, is routed the same way. It
// is called outside the store's lock and may look at the panes.
func (s *Store) SetRouter(fn func(pane string) string) {
	s.mu.Lock()
	s.router = fn
	s.mu.Unlock()
}

// Rev is a number that changes with every change to any inbox.
func (s *Store) Rev() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

// OnChange installs the function called after each change worth saving. It must
// not call back into the store.
func (s *Store) OnChange(fn func()) {
	s.mu.Lock()
	s.changed = fn
	s.mu.Unlock()
}

// OnEvent installs the function told of each message sent and each delivery
// state a message reaches, for the event log. It must not call back into the
// store.
func (s *Store) OnEvent(fn func(Event)) {
	s.mu.Lock()
	s.event = fn
	s.mu.Unlock()
}

// emit tells the event function of events, outside the lock.
func (s *Store) emit(events ...Event) {
	s.mu.Lock()
	fn := s.event
	s.mu.Unlock()
	if fn == nil {
		return
	}
	for _, e := range events {
		fn(e)
	}
}

func (s *Store) notify() {
	s.mu.Lock()
	s.rev++
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
	s.mu.Lock()
	router := s.router
	s.mu.Unlock()
	route := ""
	if router != nil {
		route = router(to)
	}
	return s.SendRoute(from, to, text, route)
}

// SendRoute is Send for a message with a route given by the caller: RoutePrompt
// for a pane that is sent its mail as prompts. The route is fixed when the
// message is made.
func (s *Store) SendRoute(from Sender, to, text, route string) (Message, error) {
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
	m := Message{ID: s.next, From: from, To: to, Time: s.now(), Text: text, Route: route}
	s.next++
	s.boxes[to] = append(box, m)
	s.mu.Unlock()
	s.emit(Event{Kind: EventMessage, ID: m.ID, From: from.Pane, To: to, Time: m.Time})
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
		if m.Finished() {
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
		if all || m.Unread() {
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
		stamped := box[i].ReadAt.IsZero() && changed
		if stamped {
			box[i].ReadAt = s.now()
		}
		m := box[i]
		s.mu.Unlock()
		if stamped {
			s.emit(Event{Kind: EventDelivery, ID: m.ID, State: DeliveryRead, Time: m.ReadAt})
		}
		if changed {
			s.notify()
		}
		return m, nil
	}
	s.mu.Unlock()
	return Message{}, fmt.Errorf("no message #%d in your inbox; `flockdeck inbox -all` lists what is there", id)
}

// MarkSeen sets the window's Seen mark on one of a pane's messages and returns
// it. It leaves Read alone, so the pane still finds the message unread.
func (s *Store) MarkSeen(pane string, id int) (Message, error) {
	s.mu.Lock()
	box := s.boxes[pane]
	for i := range box {
		if box[i].ID != id {
			continue
		}
		changed := !box[i].Seen
		box[i].Seen = true
		m := box[i]
		s.mu.Unlock()
		if changed {
			s.notify()
		}
		return m, nil
	}
	s.mu.Unlock()
	return Message{}, fmt.Errorf("no message #%d in that inbox", id)
}

// MarkNotified records that the hook notice of the pane's messages with these
// ids was given to its agent. A message that was already notified keeps its
// first time, and an id that is not in the inbox is skipped.
func (s *Store) MarkNotified(pane string, ids ...int) {
	s.mark(pane, DeliveryNotified, ids)
}

// MarkWoken records that a line was typed into the pane to wake its agent for
// the messages with these ids. A message already woken keeps its first time.
func (s *Store) MarkWoken(pane string, ids ...int) {
	s.mark(pane, DeliveryWoken, ids)
}

// mark sets the time of one delivery state on the messages with the ids.
func (s *Store) mark(pane, state string, ids []int) {
	var events []Event
	s.mu.Lock()
	box := s.boxes[pane]
	for i := range box {
		if !slices.Contains(ids, box[i].ID) {
			continue
		}
		at := &box[i].NotifiedAt
		if state == DeliveryWoken {
			at = &box[i].WokenAt
		}
		if !at.IsZero() {
			continue
		}
		*at = s.now()
		events = append(events, Event{Kind: EventDelivery, ID: box[i].ID, State: state, Time: *at})
	}
	s.mu.Unlock()
	if len(events) == 0 {
		return
	}
	s.emit(events...)
	s.notify()
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

// SnapshotVersion is the version Snapshot writes. Version 2 added the fields of
// the prompt route (Route, DeliveredAt, EndedAt, GaveUpAt, StopReason,
// Attempts). Load reads every version: an older snapshot has none of them, and
// its messages are on the terminal route, which is what they were.
const SnapshotVersion = 2

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
	return json.MarshalIndent(snapshot{Version: SnapshotVersion, Next: s.next, Boxes: boxes}, "", "  ")
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
			if m.ID <= 0 || m.To != pane || len(m.Text) > MaxText || (m.Finished() && m.Time.Before(cutoff)) || m.Time.Before(oldest) {
				continue
			}
			next = max(next, m.ID+1)
			m = restored(m, s.now())
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
	s.rev++
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
		if box[i].Unread() {
			return box[i].ID
		}
	}
	return 0
}

// restored is a loaded message as it stands after a restart. A prompt that was
// delivered and whose turn had not ended was running in an agent process that
// is gone, so the message is queued again, for the new process to be sent; if it
// has already had MaxAttempts turns it is given up on instead. A route this
// version does not know is read as the terminal route, which is the one that
// asks nothing of the pane.
func restored(m Message, now time.Time) Message {
	if m.Route != RoutePrompt {
		m.Route = ""
		return m
	}
	m.Attempts = max(m.Attempts, 0)
	if m.Delivery() != DeliveryDelivered {
		return m
	}
	m.DeliveredAt = time.Time{}
	m.StopReason = StopInterrupted
	if m.Attempts >= MaxAttempts {
		m.GaveUpAt = now
	}
	return m
}

// Pending returns the messages of a pane that are queued on the prompt route,
// oldest first, as copies.
func (s *Store) Pending(pane string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Message
	for _, m := range s.boxes[pane] {
		if m.Route == RoutePrompt && m.Delivery() == DeliveryQueued {
			out = append(out, m)
		}
	}
	return out
}

// InFlight returns the messages of a pane that are delivered and whose turn has
// not ended, oldest first, as copies.
func (s *Store) InFlight(pane string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Message
	for _, m := range s.boxes[pane] {
		if m.Route == RoutePrompt && m.Delivery() == DeliveryDelivered {
			out = append(out, m)
		}
	}
	return out
}

// Panes returns the ids of the panes that have a message on the prompt route
// that is queued or delivered, for a service that works through them.
func (s *Store) Panes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for pane, box := range s.boxes {
		for _, m := range box {
			if d := m.Delivery(); m.Route == RoutePrompt && (d == DeliveryQueued || d == DeliveryDelivered) {
				out = append(out, pane)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// find returns the index of the message in the pane's inbox, or -1.
func (s *Store) find(pane string, id int) int {
	for i, m := range s.boxes[pane] {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// Deliver records that the message was sent to the pane's agent as a prompt and
// its turn began. Only a queued message on the prompt route can be delivered,
// and it counts as an attempt; false means it was not queued, so nothing was
// done.
func (s *Store) Deliver(pane string, id int) (Message, bool) {
	s.mu.Lock()
	i := s.find(pane, id)
	if i < 0 || s.boxes[pane][i].Route != RoutePrompt || s.boxes[pane][i].Delivery() != DeliveryQueued {
		s.mu.Unlock()
		return Message{}, false
	}
	m := &s.boxes[pane][i]
	m.DeliveredAt = s.now()
	m.Attempts++
	out := *m
	s.mu.Unlock()
	s.emit(Event{Kind: EventDelivery, ID: out.ID, State: DeliveryDelivered, Time: out.DeliveredAt, Attempt: out.Attempts})
	s.notify()
	return out, true
}

// EndTurn records how the turn that carried a delivered message ended. The stop
// reason is stored as Outcome gives it. A handled message is done; any other goes
// back to the queue to be sent again, unless it has had MaxAttempts turns, when
// it is given up on. A message that is not delivered is left alone.
func (s *Store) EndTurn(pane string, id int, stop string) (Message, bool) {
	reason, handled := Outcome(stop)
	return s.end(pane, id, reason, handled)
}

// Interrupted records that the turn carrying a delivered message did not end:
// the agent exited, the pane was restarted, or the prompt was refused with an
// error. The message goes back to the queue, or is given up on after
// MaxAttempts.
func (s *Store) Interrupted(pane string, id int) (Message, bool) {
	return s.end(pane, id, StopInterrupted, false)
}

func (s *Store) end(pane string, id int, reason string, handled bool) (Message, bool) {
	s.mu.Lock()
	i := s.find(pane, id)
	if i < 0 || s.boxes[pane][i].Route != RoutePrompt || s.boxes[pane][i].Delivery() != DeliveryDelivered {
		s.mu.Unlock()
		return Message{}, false
	}
	m := &s.boxes[pane][i]
	now := s.now()
	m.StopReason = reason
	switch {
	case handled:
		m.EndedAt = now
	case m.Attempts >= MaxAttempts:
		m.DeliveredAt = time.Time{}
		m.GaveUpAt = now
	default:
		m.DeliveredAt = time.Time{}
	}
	out := *m
	s.mu.Unlock()
	s.emit(Event{Kind: EventDelivery, ID: out.ID, State: out.Delivery(), Time: now, Stop: reason, Attempt: out.Attempts})
	s.notify()
	return out, true
}
