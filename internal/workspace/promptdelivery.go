package workspace

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// The prompt delivery service sends a structured pane its mail as session/prompt
// requests. A message to a structured pane is stored with the prompt route (see
// team.Message.Route) and stays queued in the store until the service hands it to
// the pane's runtime, which holds at most 16 prompts behind the running turn. A
// full runtime queue is the spill: the rest wait in the store and are handed over
// as room appears. The store is the audit log; the runtime's queue is a cache of
// what it has been given, and is lost with the process.
//
// Which prompt a result belongs to is decided by the message id the prompt was
// sent with (its tag) and by the generation of the runtime it was sent to, never
// by order: a runtime that has been replaced by a restart can still report late,
// and what it reports is ignored.
//
// The off switch of the wake service (WakeEnabled) pauses handing messages over,
// so a user can stop an agent being prompted by its peers; the messages stay
// queued and visible to their senders.

const promptTick = time.Second

// promptNoText stands for a body with nothing readable left in it.
const promptNoText = "(no readable text)"

// promptBodyMarker starts the second block, so that a body which opens like a
// prefix ("Flockdeck message from the user: ...") reads as the sender's text
// and not as a second line from Flockdeck when an adapter joins the blocks.
const promptBodyMarker = "Text of the message: "

// promptBlocks are the two text content blocks a message is sent as. The first
// is fixed text and the sender's role or name; the second is promptBodyMarker and
// the message body, cleaned to one line (see cleanWakeText), so that nothing in
// it can start a line of its own, and no block ever starts with the prefix
// whatever the body says. The sender is shown as the person only for a
// message the person wrote, and every other sender is an agent with its label in
// quotes, so a role or name of "the user" reads as agent "the user".
func promptBlocks(m team.Message) []string {
	text := cleanWakeText(m.Text)
	if text == "" {
		text = promptNoText
	}
	return []string{wakeTextPrefix + wakePromptSender(m) + ":", promptBodyMarker + text}
}

// promptPane is one structured pane as the service sees it.
type promptPane struct {
	ID    string
	Agent string
	// Gen identifies the runtime. It differs after a restart.
	Gen   uint64
	Alive func() bool
	Send  func(tag int, blocks ...string) error
}

// promptMail is what the service needs of the inboxes. *team.Store is one.
type promptMail interface {
	Panes() []string
	Pending(pane string) []team.Message
	InFlight(pane string) []team.Message
	Deliver(pane string, id int) (team.Message, bool)
	EndTurn(pane string, id int, stop string) (team.Message, bool)
	Interrupted(pane string, id int) (team.Message, bool)
	EndTurnAsked(pane string, id int, stop string) (team.Message, bool)
	Fail(pane string, id int) (team.Message, bool)
	Reroute(pane string) int
}

type promptService struct {
	mail     promptMail
	panes    func() map[string]promptPane
	settings func() WakeSettings
	// terminal says the pane exists and runs as a terminal (or is not an agent
	// pane Flockdeck can run as a structured one), so mail on the prompt route to
	// it can never be sent as a prompt. Nil means no pane is.
	terminal func(pane string) bool

	mu sync.Mutex
	// handed is the messages given to a runtime and not yet reported on, by pane,
	// with the generation of the runtime each was given to.
	handed map[string]map[int]uint64

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newPromptService(mail promptMail, panes func() map[string]promptPane, settings func() WakeSettings) *promptService {
	return &promptService{
		mail: mail, panes: panes, settings: settings,
		handed: map[string]map[int]uint64{},
		stop:   make(chan struct{}),
	}
}

func (s *promptService) run() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(promptTick)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.tick()
			}
		}
	}()
}

// Stop ends the service.
func (s *promptService) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// tick works through every pane that has mail on the prompt route that is queued
// or delivered.
func (s *promptService) tick() {
	panes := s.panes()
	for _, id := range s.mail.Panes() {
		c, ok := panes[id]
		if !ok && s.terminal != nil && s.terminal(id) {
			// Not a structured pane and not going to be: its mail moves to the
			// terminal route, where the wake and the inbox deal with it.
			s.mail.Reroute(id)
			continue
		}
		s.pane(id, c)
	}
	// What is remembered of a pane that is gone goes with it.
	s.mu.Lock()
	for id := range s.handed {
		if _, ok := panes[id]; !ok {
			delete(s.handed, id)
		}
	}
	s.mu.Unlock()
}

// pane settles one pane: a delivered message whose runtime is not the one that
// would report on it goes back to the queue, and the queued ones are handed over
// while there is room. A pane with no runtime is the zero promptPane.
func (s *promptService) pane(id string, c promptPane) {
	live := c.Alive != nil && c.Alive()
	for _, m := range s.mail.InFlight(id) {
		gen, ok := s.generation(id, m.ID)
		if !live || !ok || gen != c.Gen {
			s.mail.Interrupted(id, m.ID)
			s.forget(id, m.ID)
		}
	}
	if !live || !s.settings().WakeEnabled(c.Agent, id) {
		return
	}
	for _, m := range s.mail.Pending(id) {
		if gen, ok := s.generation(id, m.ID); ok && gen == c.Gen {
			continue
		}
		// Recorded before the send: the runtime may report the turn's start
		// before Send has returned.
		s.mu.Lock()
		if s.handed[id] == nil {
			s.handed[id] = map[int]uint64{}
		}
		s.handed[id][m.ID] = c.Gen
		s.mu.Unlock()
		if err := c.Send(m.ID, promptBlocks(m)...); err != nil {
			s.forget(id, m.ID)
			if errors.Is(err, structured.ErrQueueFull) || errors.Is(err, structured.ErrNotRunning) {
				return
			}
			// Something about this message the runtime will not take. It counts
			// as an attempt, so it ends undeliverable and not retried for ever,
			// and the pane's other messages go on.
			s.mail.Fail(id, m.ID)
		}
	}
}

func (s *promptService) generation(pane string, id int) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen, ok := s.handed[pane][id]
	return gen, ok
}

func (s *promptService) forget(pane string, id int) {
	s.mu.Lock()
	delete(s.handed[pane], id)
	s.mu.Unlock()
}

// turn is told what became of a prompt by the runtime of generation gen. A report
// about a message that was not handed to that runtime is ignored.
func (s *promptService) turn(pane string, gen uint64, e structured.TurnEvent) {
	if cur, ok := s.generation(pane, e.Tag); !ok || cur != gen {
		return
	}
	if e.What == structured.TurnBegan {
		s.mail.Deliver(pane, e.Tag)
		return
	}
	if e.What == structured.TurnEnded {
		if e.Asked {
			s.mail.EndTurnAsked(pane, e.Tag, e.Stop)
		} else {
			s.mail.EndTurn(pane, e.Tag, e.Stop)
		}
	} else if e.What == structured.TurnLost {
		s.mail.Interrupted(pane, e.Tag)
	} else {
		return
	}
	s.forget(pane, e.Tag)
}

// runtimeGen numbers the structured runtimes of the process.
var runtimeGen atomic.Uint64

// promptPanes lists the structured panes that have a runtime.
func (w *Workspace) promptPanes() map[string]promptPane {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := map[string]promptPane{}
	for _, p := range w.panes {
		sr, ok := w.Runtime(p).(*structuredRuntime)
		if !ok {
			continue
		}
		out[p.ID] = promptPane{ID: p.ID, Agent: p.Agent, Gen: sr.gen, Alive: sr.Alive,
			Send: sr.rt.SendTagged}
	}
	return out
}

// promptTurn is where a structured runtime reports what became of a prompt of
// the pane id.
func (w *Workspace) promptTurn(id string, gen uint64, e structured.TurnEvent) {
	if s := w.promptSvc; s != nil {
		s.turn(id, gen, e)
	}
}

// startPromptDelivery starts the service on the workspace's panes.
func (w *Workspace) startPromptDelivery() {
	t := &w.team
	if t.inbox == nil {
		return
	}
	s := newPromptService(t.inbox, w.promptPanes, func() WakeSettings { return w.wakeSettings })
	s.terminal = w.paneIsTerminal
	w.promptSvc = s
	s.run()
}

func (w *Workspace) stopPromptDelivery() {
	if w.promptSvc != nil {
		w.promptSvc.Stop()
	}
}

// promptRouter is the store's router: mail to a structured pane, or to a pane
// that was saved as one and has not started yet, is sent as prompts.
func (w *Workspace) promptRouter(id string) string {
	w.mu.RLock()
	p := w.panes[id]
	saved := p != nil && p.savedTransport == TransportStructured
	w.mu.RUnlock()
	if p != nil && (saved || p.Structured()) {
		return team.RoutePrompt
	}
	return ""
}

// paneIsTerminal says the pane is open, is not structured and was not saved as
// structured, so it will never take prompts. A pane that is between runtimes, as
// at a restart, or whose structured agent failed to start, is not.
func (w *Workspace) paneIsTerminal(id string) bool {
	w.mu.RLock()
	p := w.panes[id]
	saved := p != nil && p.savedTransport == TransportStructured
	w.mu.RUnlock()
	return p != nil && !saved && !p.Structured()
}
