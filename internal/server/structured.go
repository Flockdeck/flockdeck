package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// This file is what a window is told about a structured pane: structuredSnapshot,
// structuredWatch and structuredUnwatch, and what it may ask of it:
// structuredCancel, and structuredSet for the mode, model and settings. They carry the pane's state (phase, status, activity, plan,
// usage, the permission card and setup problems) and no conversation: the
// conversation is read with conversationOpen, conversationOlder and
// conversationDetail, as for any pane.
//
// All of them are for a window on this machine. A window reached through the relay
// is refused, a full device included, and a viewer is refused by the table of
// commands (commandAccess), not let in as a watcher: the push carries tool
// commands, file paths and reply text that are clipped but not scrubbed, and a
// structured pane has no sealed channel for them as a terminal pane has. The
// refusals are one place each, marked as the place to lift when that changes.
//
// The reference is docs/structured-pane.md and the shapes are in
// api/schema/structured-pane.schema.json.

// Limits on what is sent. Every cut is made on a character boundary, never in
// the middle of a UTF-8 sequence.
const (
	// structuredLabelCut bounds one-line fields such as the activity.
	structuredLabelCut = 1 << 10
	// structuredPlanMax and structuredPlanCut bound the plan.
	structuredPlanMax = 100
	structuredPlanCut = 200
	// structuredCardCut bounds one field of the permission card, and
	// structuredCommandCut its command.
	structuredCardCut    = 300
	structuredCommandCut = 500
	// structuredErrCut is how many characters the cleaned error keeps.
	structuredErrCut = 200
	// The session state: how many of each is sent, and the cut of each text.
	structuredCommandsMax = 200
	structuredChoicesMax  = 500
	structuredConfigMax   = 32
	structuredValuesMax   = 500
	structuredDescCut     = 400
	structuredMarkdownCut = 64 << 10
	// maxStructuredWatches is how many panes may be watched at once, over all
	// windows.
	maxStructuredWatches = 16
	// structuredInterval is the least time between two pushes: two a second.
	structuredInterval = 500 * time.Millisecond
)

const (
	structuredDeskOnly = "A structured pane is used on the machine Flockdeck runs on, not from a window reached through the relay"
	structuredNotPane  = "That pane is not a structured pane"
	structuredTooMany  = "Too many structured panes are being watched. Stop watching one first"
	// structuredIdle is what structuredCancel says when the pane has no turn to stop.
	structuredIdle = "That pane has nothing running to stop"
	// The refusals of structuredSet. None of them carries text from the agent.
	structuredNoSetting   = "That setting is not on offer any more. Pick one from the list again"
	structuredSetNotReady = "That pane's agent is not running"
	structuredSetFailed   = "The agent did not change the setting"
	structuredSetKind     = "structuredSet needs a kind of mode, model or config"
)

type structuredPlanRow struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type structuredOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// structuredCard is the permission request that is open. It is answered with
// permissionAnswer, which is also for a window on this machine only.
type structuredCard struct {
	Seq       uint64             `json:"seq"`
	RequestID string             `json:"requestId"`
	Tool      string             `json:"tool,omitempty"`
	Kind      string             `json:"kind,omitempty"`
	Summary   string             `json:"summary,omitempty"`
	File      string             `json:"file,omitempty"`
	Command   string             `json:"command,omitempty"`
	Options   []structuredOption `json:"options"`
	Clipped   bool               `json:"clipped,omitempty"`
}

type structuredUsage struct {
	Used     int64   `json:"used"`
	Size     int64   `json:"size"`
	Cost     float64 `json:"cost,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

type structuredSetup struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// structuredCommand is one slash command the agent offers.
type structuredCommandRow struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// structuredChoice is one mode or model on offer.
type structuredChoice struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// structuredChoices is a mode or model block: the current id and what is on
// offer.
type structuredChoices struct {
	Current   string             `json:"current,omitempty"`
	Available []structuredChoice `json:"available"`
}

type structuredConfigValue struct {
	Value       string `json:"value"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group,omitempty"`
}

// structuredConfigOption is one setting the agent exposes (model, effort, mode).
type structuredConfigOption struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name,omitempty"`
	Description string                  `json:"description,omitempty"`
	Category    string                  `json:"category,omitempty"`
	Control     string                  `json:"type,omitempty"`
	Current     string                  `json:"current"`
	Values      []structuredConfigValue `json:"values"`
}

// structuredCapabilities is what the agent said it can do at initialize.
type structuredCapabilities struct {
	LoadSession     bool `json:"loadSession"`
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
	MCPHTTP         bool `json:"mcpHttp"`
	MCPSSE          bool `json:"mcpSse"`
	ListSessions    bool `json:"listSessions"`
	ResumeSession   bool `json:"resumeSession"`
	ForkSession     bool `json:"forkSession"`
}

// structuredMsg is what structuredSnapshot answers with and what a watching
// window is pushed. Gone is the last message of a watch whose pane has closed.
type structuredMsg struct {
	Type     string                `json:"type"`
	Req      string                `json:"req,omitempty"`
	ID       string                `json:"id"`
	Kind     string                `json:"kind"`
	Gone     bool                  `json:"gone,omitempty"`
	Phase    string                `json:"phase,omitempty"`
	Status   string                `json:"status,omitempty"`
	Activity string                `json:"activity,omitempty"`
	Queued   int                   `json:"queued,omitempty"`
	Queue    []structuredQueueItem `json:"queue,omitempty"`
	Err      string                `json:"err,omitempty"`
	Setup    *structuredSetup      `json:"setup,omitempty"`
	Plan     []structuredPlanRow   `json:"plan"`
	Card     *structuredCard       `json:"card,omitempty"`
	Usage    structuredUsage       `json:"usage"`

	Title        string                   `json:"title,omitempty"`
	Commands     []structuredCommandRow   `json:"commands,omitempty"`
	Modes        *structuredChoices       `json:"modes,omitempty"`
	Models       *structuredChoices       `json:"models,omitempty"`
	ConfigOpts   []structuredConfigOption `json:"configOptions,omitempty"`
	PlanMarkdown string                   `json:"planMarkdown,omitempty"`
	Capabilities *structuredCapabilities  `json:"capabilities,omitempty"`
}

// clipStructured makes s fit for a window and cuts it to at most max bytes. It
// replaces bytes that are not UTF-8, removes control characters other than a
// line break and a tab, and cuts only between characters. It says whether
// anything was cut.
func clipStructured(s string, max int) (string, bool) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, s)
	}
	if len(s) <= max {
		return s, false
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// clipLine is clipStructured for a field that is one line: a line break is a
// space.
func clipLine(s string, max int) (string, bool) {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	return clipStructured(s, max)
}

// buildStructured is the message for one runtime's state. It reads nothing but
// the snapshot it is given, which is that one pane's: no other pane's text, no
// mail, and nothing of the agent's environment is in it.
func buildStructured(id string, sn structured.Snapshot) structuredMsg {
	m := structuredMsg{Type: "structured", ID: id, Kind: "structured",
		Phase: string(sn.Phase), Status: string(sn.Status), Queued: sn.Queued,
		Plan:  []structuredPlanRow{},
		Usage: structuredUsage{Used: sn.Usage.Used, Size: sn.Usage.Size, Cost: sn.Usage.Cost, Currency: sn.Usage.Currency}}
	m.Activity, _ = clipLine(sn.Activity, structuredLabelCut)
	m.Err = team.CleanLine(sn.Err, structuredErrCut)
	if s := sn.Setup; s != nil {
		msg, _ := clipLine(s.Message, structuredCardCut)
		fix, _ := clipLine(s.Login, structuredCardCut)
		m.Setup = &structuredSetup{Kind: s.Kind, Message: msg, Fix: fix}
	}
	for i, p := range sn.Plan {
		if i == structuredPlanMax {
			break
		}
		row := structuredPlanRow{}
		row.Content, _ = clipLine(p.Content, structuredPlanCut)
		row.Priority, _ = clipLine(p.Priority, 40)
		row.Status, _ = clipLine(p.Status, 40)
		m.Plan = append(m.Plan, row)
	}
	if c := sn.Pending; c != nil {
		card := &structuredCard{Seq: c.Seq, RequestID: c.RequestID, Options: []structuredOption{}}
		var cl bool
		field := func(p *string, from string, max int) {
			var c bool
			*p, c = clipLine(from, max)
			cl = cl || c
		}
		field(&card.RequestID, c.RequestID, structuredCardCut)
		field(&card.Tool, c.Tool, structuredCardCut)
		field(&card.Kind, c.Kind, structuredCardCut)
		field(&card.Summary, c.Summary, structuredCardCut)
		field(&card.File, c.File, structuredCardCut)
		field(&card.Command, c.Command, structuredCommandCut)
		for _, o := range c.Options {
			var opt structuredOption
			field(&opt.ID, o.OptionID, 100)
			field(&opt.Name, o.Name, 100)
			field(&opt.Kind, o.Kind, 100)
			card.Options = append(card.Options, opt)
		}
		card.Clipped = cl
		m.Card = card
	}

	buildStructuredQueue(&m, sn)
	buildStructuredSession(&m, sn)
	return m
}

// buildStructuredSession adds what the agent exposes for the session. It sends
// nothing the agent did not offer, so a field is absent rather than empty.
func buildStructuredSession(m *structuredMsg, sn structured.Snapshot) {
	ss := sn.Session
	m.Title, _ = clipLine(ss.Title, structuredCardCut)
	m.PlanMarkdown, _ = clipStructured(ss.PlanMarkdown, structuredMarkdownCut)
	for i, c := range ss.Commands {
		if i == structuredCommandsMax {
			break
		}
		row := structuredCommandRow{}
		row.Name, _ = clipLine(c.Name, structuredCardCut)
		row.Description, _ = clipLine(c.Description, structuredDescCut)
		row.Hint, _ = clipLine(c.Hint, structuredCardCut)
		m.Commands = append(m.Commands, row)
	}
	if b := ss.Modes; b != nil {
		ch := &structuredChoices{Available: []structuredChoice{}}
		ch.Current, _ = clipLine(b.Current, structuredCardCut)
		for i, v := range b.Available {
			if i == structuredChoicesMax {
				break
			}
			c := structuredChoice{}
			c.ID, _ = clipLine(v.ID, structuredCardCut)
			c.Name, _ = clipLine(v.Name, structuredCardCut)
			c.Description, _ = clipLine(v.Description, structuredDescCut)
			ch.Available = append(ch.Available, c)
		}
		m.Modes = ch
	}
	if b := ss.Models; b != nil {
		ch := &structuredChoices{Available: []structuredChoice{}}
		ch.Current, _ = clipLine(b.Current, structuredCardCut)
		for i, v := range b.Available {
			if i == structuredChoicesMax {
				break
			}
			c := structuredChoice{}
			c.ID, _ = clipLine(v.ID, structuredCardCut)
			c.Name, _ = clipLine(v.Name, structuredCardCut)
			c.Description, _ = clipLine(v.Description, structuredDescCut)
			ch.Available = append(ch.Available, c)
		}
		m.Models = ch
	}
	for i, o := range ss.ConfigOptions {
		if i == structuredConfigMax {
			break
		}
		kind, _ := clipLine(o.Type, 40)
		opt := structuredConfigOption{Values: []structuredConfigValue{}, Control: kind}
		opt.ID, _ = clipLine(o.ID, structuredCardCut)
		opt.Name, _ = clipLine(o.Name, structuredCardCut)
		opt.Description, _ = clipLine(o.Description, structuredDescCut)
		opt.Category, _ = clipLine(o.Category, 40)
		opt.Current, _ = clipLine(o.Current, structuredCardCut)
		for j, v := range o.Values {
			if j == structuredValuesMax {
				break
			}
			val := structuredConfigValue{}
			val.Value, _ = clipLine(v.Value, structuredCardCut)
			val.Name, _ = clipLine(v.Name, structuredCardCut)
			val.Description, _ = clipLine(v.Description, structuredDescCut)
			val.Group, _ = clipLine(v.Group, structuredCardCut)
			opt.Values = append(opt.Values, val)
		}
		m.ConfigOpts = append(m.ConfigOpts, opt)
	}
	if sn.Phase == structured.PhaseReady {
		c := ss.Capabilities
		m.Capabilities = &structuredCapabilities{LoadSession: c.LoadSession, Image: c.Prompt.Image, Audio: c.Prompt.Audio,
			EmbeddedContext: c.Prompt.EmbeddedContext, MCPHTTP: c.MCP.HTTP, MCPSSE: c.MCP.SSE,
			ListSessions: c.List, ResumeSession: c.Resume, ForkSession: c.Fork}
	}
}

// structuredSnapshotOf is the state of the structured pane id for a window: the
// runtime's own snapshot with the conversation the pane shows, which is the
// sessions before its last restart and then the current one. The runtime knows
// only the current session, so a window given its snapshot alone would lose the
// earlier conversation at a restart. It must run on the workspace goroutine.
func (s *Server) structuredSnapshotOf(id string) (structured.Snapshot, bool) {
	return s.ws.StructuredSnapshot(id)
}

// structuredCommand answers the structured pane commands.
func (s *Server) structuredCommand(c *controlClient, cmd command) {
	// structured-pane-relay: the refusal to lift when a sealed channel exists.
	if c.remote {
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, Error: structuredDeskOnly})
		c.notify(structuredDeskOnly, true)
		return
	}
	switch cmd.Cmd {
	case "structuredSnapshot":
		type out struct {
			data []byte
			ok   bool
		}
		r, ok := ask(s, func() out {
			rt := s.ws.StructuredRuntime(cmd.ID)
			if rt == nil {
				return out{}
			}
			sn, _ := s.structuredSnapshotOf(cmd.ID)
			m := buildStructured(cmd.ID, sn)
			m.Req = cmd.Req
			data, err := json.Marshal(m)
			return out{data, err == nil}
		})
		if !ok {
			return
		}
		if !r.ok {
			refuse(c, cmd, errStructured(structuredNotPane))
			return
		}
		c.send(r.data)
	case "structuredCancel":
		s.do(func() { s.structuredCancel(c, cmd) })
	case "structuredSet":
		s.do(func() { s.structuredSet(c, cmd) })
	case "structuredFiles":
		s.do(func() { s.structuredFiles(c, cmd) })
	case "structuredQueueEdit", "structuredQueueRemove", "structuredQueueClear", "structuredQueueSendNow":
		s.do(func() { s.structuredQueue(c, cmd) })
	case "structuredWatch":
		s.do(func() { s.structuredWatch(c, cmd) })
	case "structuredUnwatch":
		s.do(func() { c.structID, c.structSent = "", nil })
	}
}

type errStructured string

func (e errStructured) Error() string { return string(e) }

// structuredCancel ends the running turn of the one structured pane named by
// cmd.ID, and drops the prompts waiting behind it unless cmd.KeepQueue is set; an open permission request
// of that pane is answered cancelled. It touches that pane's own runtime and
// nothing else. A window reached through the relay was refused above, and a
// viewer by the table of commands. The answer is a teamResult for the op.
func (s *Server) structuredCancel(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	stop := rt.Cancel
	if cmd.KeepQueue {
		stop = rt.Interrupt
	}
	if err := stop(); err != nil {
		if errors.Is(err, structured.ErrNotRunning) {
			refuse(c, cmd, errStructured(structuredIdle))
			return
		}
		refuse(c, cmd, errStructured("The turn could not be stopped: "+cleanForNotice(err.Error())))
		return
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
}

// structuredSet changes the mode, the model or one setting of the structured pane
// cmd.ID: Kind is "mode", "model" or "config", Value the id of the mode or
// model or the value of the option, and Setting the option's id for "config".
// Only what the pane last pushed is accepted. The agent is asked on a goroutine
// of its own, since it may take a while, and the answer is a teamResult for the
// op. Whatever the agent says on a refusal is not passed on. It must run on the
// workspace goroutine.
func (s *Server) structuredSet(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	var set func() error
	switch cmd.Kind {
	case "mode":
		set = func() error { return rt.SetMode(cmd.Value) }
	case "model":
		set = func() error { return rt.SetModel(cmd.Value) }
	case "config":
		set = func() error { return rt.SetConfig(cmd.Setting, cmd.Value) }
	default:
		refuse(c, cmd, errStructured(structuredSetKind))
		return
	}
	go func() {
		err := set()
		switch {
		case err == nil:
			c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
		case errors.Is(err, structured.ErrNoSuchSetting):
			refuse(c, cmd, errStructured(structuredNoSetting))
		case errors.Is(err, structured.ErrNotRunning):
			refuse(c, cmd, errStructured(structuredSetNotReady))
		default:
			refuse(c, cmd, errStructured(structuredSetFailed))
		}
	}()
}

// structuredWatch subscribes the window to one pane, replacing any it watched.
// It must run on the workspace goroutine.
func (s *Server) structuredWatch(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		c.structID, c.structSent = "", nil
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	watched := map[string]bool{cmd.ID: true}
	for _, o := range s.greetedClients() {
		if o != c && o.structID != "" {
			watched[o.structID] = true
		}
	}
	if len(watched) > maxStructuredWatches {
		c.structID, c.structSent = "", nil
		refuse(c, cmd, errStructured(structuredTooMany))
		return
	}
	sn, _ := s.structuredSnapshotOf(cmd.ID)
	m := buildStructured(cmd.ID, sn)
	plain, err := json.Marshal(m)
	if err != nil {
		return
	}
	m.Req = cmd.Req
	first, err := json.Marshal(m)
	if err != nil {
		return
	}
	c.structID, c.structSent = cmd.ID, nil
	if c.trySend(first) {
		c.structSent = plain
		// The reply counts as a push, so a change within the interval waits for
		// the interval instead of making a second one at once.
		s.structAt = time.Now()
	} else {
		s.armStructured()
	}
}

// armStructured arranges for a push in a moment, once.
func (s *Server) armStructured() {
	if s.structArmed {
		return
	}
	s.structArmed = true
	wait := max(structuredInterval-time.Since(s.structAt), 50*time.Millisecond)
	time.AfterFunc(wait, func() {
		s.do(func() {
			s.structArmed = false
			s.pushStructured()
		})
	})
}

// pushStructured sends each window watching a structured pane that pane's state
// when it has changed, at most twice a second; a push that comes too soon is
// made when the interval is up. It runs on the workspace goroutine and is called
// with each state push. A pane that is gone is told to the window once, as a
// message with gone set, and the watch ends. A window whose queue was full is
// sent the state again rather than the push being skipped as unchanged.
func (s *Server) pushStructured() {
	clients := s.greetedClients()
	watching := false
	for _, c := range clients {
		if c.structID != "" {
			watching = true
		}
	}
	if !watching {
		return
	}
	if time.Since(s.structAt) < structuredInterval {
		s.armStructured()
		return
	}
	s.structAt = time.Now()
	cache := map[string][]byte{}
	retry := false
	for _, c := range clients {
		id := c.structID
		if id == "" {
			continue
		}
		data, ok := cache[id]
		if !ok {
			if sn, ok := s.structuredSnapshotOf(id); ok {
				data, _ = json.Marshal(buildStructured(id, sn))
			} else {
				data, _ = json.Marshal(structuredMsg{Type: "structured", ID: id, Kind: "structured", Gone: true,
					Plan: []structuredPlanRow{}})
			}
			cache[id] = data
		}
		if bytes.Equal(data, c.structSent) {
			continue
		}
		if c.trySend(data) {
			c.structSent = data
			if s.ws.StructuredRuntime(id) == nil {
				c.structID, c.structSent = "", nil
			}
		} else {
			retry = true
		}
	}
	if retry {
		s.armStructured()
	}
}
