package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/changes"
	"github.com/jmwri/flockdeck/internal/tasks"
)

// The team-wide branch list (docs/changes-api.md, sections 2 and 3): changesWatch,
// changesUnwatch, changesSnapshot and filesPage. The git work is in
// internal/changes and runs on a goroutine of the window's own, never on the
// workspace goroutine, which is only asked which panes there are.

const (
	// changesPoll is how often a watch looks at the checkouts. A push is sent only
	// when what it holds changed, so a window is sent at most one every poll.
	changesPoll = 3 * time.Second
	// changesReadLimit bounds one read of every checkout.
	changesReadLimit = 90 * time.Second

	filesPageDefault = 200
	filesPageMax     = 500
)

// changesTeamMsg and changesFilesMsg are the messages of the contract: the type
// is written here, where the relay classification can see it, round what
// internal/changes read.
type changesTeamMsg struct {
	Type string `json:"type"`
	changes.Team
}

type changesFilesMsg struct {
	Type string `json:"type"`
	changes.Files
}

type diffHunksMsg struct {
	Type string `json:"type"`
	changes.DiffHunks
}

type diffContextMsg struct {
	Type string `json:"type"`
	changes.DiffContext
}

// changesWatcher is a window's watch: stopping it ends its goroutine.
type changesWatcher struct {
	root   string
	cancel context.CancelFunc
	// last is the newest read of the team, which filesPage is served from, so a
	// window paging through a branch does not read the checkout again for each page.
	last atomic.Pointer[changes.Team]
}

const (
	changesBusySnapshot uint32 = 1 << iota
	changesBusyPage
	changesBusyDiff
	changesBusyContext
)

// beginRead marks a read of kind bit as running for the window, and says false
// when one already is.
func (c *controlClient) beginRead(bit uint32) bool {
	for {
		old := c.changesBusy.Load()
		if old&bit != 0 {
			return false
		}
		if c.changesBusy.CompareAndSwap(old, old|bit) {
			return true
		}
	}
}

func (c *controlClient) endRead(bit uint32) {
	for {
		old := c.changesBusy.Load()
		if c.changesBusy.CompareAndSwap(old, old&^bit) {
			return
		}
	}
}

const changesBusyText = "An earlier request of this kind is still being answered"

// changesTarget is what the workspace goroutine is asked for: the project's root
// and name, and the agent panes whose checkouts are to be read.
type changesTarget struct {
	root, project string
	panes         []changes.PaneRef
	err           error
}

func (s *Server) changesTargetOf(root string) changesTarget {
	ws := s.ws
	resolved, err := ws.TeamRoot(root)
	if err != nil {
		return changesTarget{err: err}
	}
	panes, err := ws.TeamPanes(resolved)
	if err != nil {
		return changesTarget{err: err}
	}
	members, err := ws.TeamMembers(resolved)
	if err != nil {
		return changesTarget{err: err}
	}
	agents := map[string]bool{}
	for _, m := range members {
		agents[m.Pane] = true
	}
	t := changesTarget{root: resolved}
	for _, p := range ws.Projects() {
		if p.Root == resolved {
			t.project = p.Name
		}
	}
	taskOf := s.changesTasks(resolved)
	for _, p := range panes {
		if agents[p.ID] && p.Cwd != "" {
			t.panes = append(t.panes, changes.PaneRef{ID: p.ID, Cwd: p.Cwd, TaskID: taskOf[p.ID]})
		}
	}
	return t
}

// changesTasks maps each pane of the project to the task it is working on: the
// newest of its tasks that is assigned, in progress or in review. Without a board
// it is empty.
func (s *Server) changesTasks(root string) map[string]string {
	out := map[string]string{}
	board, err := s.ws.TeamBoard(root)
	if err != nil || board == nil {
		return out
	}
	list, _, err := board.List(tasks.Filter{})
	if err != nil {
		return out
	}
	newest := map[string]time.Time{}
	for _, t := range list {
		if t.OwnerPane == "" || (t.State != tasks.Assigned && t.State != tasks.InProgress && t.State != tasks.InReview) {
			continue
		}
		if at, ok := newest[t.OwnerPane]; !ok || t.Updated.After(at) || (t.Updated.Equal(at) && t.ID > out[t.OwnerPane]) {
			newest[t.OwnerPane], out[t.OwnerPane] = t.Updated, t.ID
		}
	}
	return out
}

// changesCommand answers the commands of the team-wide branch list. A window
// through the relay is refused by changesPlanned, as for every command of the
// contract.
func (s *Server) changesCommand(c *controlClient, cmd command) {
	if c.remote {
		s.changesPlanned(c, cmd)
		return
	}
	switch cmd.Cmd {
	case "changesWatch":
		s.changesWatch(c, cmd)
	case "changesUnwatch":
		s.do(c.stopChangesWatch)
	case "changesSnapshot":
		go s.changesSnapshot(c, cmd)
	case "filesPage":
		go s.changesFilesPage(c, cmd)
	case "diffHunks":
		go s.changesDiffHunks(c, cmd)
	case "diffContext":
		go s.changesDiffContext(c, cmd)
	}
}

// changesRefuse answers a refusal. msg may quote git (see changes.Read) and is for
// a window on this machine only: a relay window is refused before anything is
// read (changesCommand), and scrubChangesResult would drop it anyway.
func changesRefuse(c *controlClient, cmd command, code, msg string) {
	c.sendJSON(changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, Code: code, Error: msg})
}

func (s *Server) changesSnapshot(c *controlClient, cmd command) {
	if !c.beginRead(changesBusySnapshot) {
		changesRefuse(c, cmd, "busy", changesBusyText)
		return
	}
	defer c.endRead(changesBusySnapshot)
	t, ok := ask(s, func() changesTarget { return s.changesTargetOf(cmd.Root) })
	if !ok {
		return
	}
	if t.err != nil {
		changesRefuse(c, cmd, "badRequest", t.err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	team := changes.ReadTeam(ctx, t.root, t.project, t.panes, 1)
	team.Req = cmd.Req
	c.sendJSON(changesTeamMsg{Type: "changesTeam", Team: team})
}

// changesWatch starts the window's watch, replacing any it had, and sends the
// first push at once.
func (s *Server) changesWatch(c *controlClient, cmd command) {
	t, ok := ask(s, func() changesTarget {
		t := s.changesTargetOf(cmd.Root)
		if t.err != nil {
			c.stopChangesWatch()
			return t
		}
		c.stopChangesWatch()
		ctx, cancel := context.WithCancel(context.Background())
		w := &changesWatcher{root: t.root, cancel: cancel}
		c.changes = w
		go s.runChangesWatch(ctx, c, w, t.root, cmd.Req)
		return t
	})
	if ok && t.err != nil {
		changesRefuse(c, cmd, "badRequest", t.err.Error())
	}
}

// stopChangesWatch ends the window's watch. It belongs to the workspace goroutine.
func (c *controlClient) stopChangesWatch() {
	if c.changes != nil {
		c.changes.cancel()
		c.changes = nil
	}
}

// runChangesWatch reads the team's checkouts every changesPoll and sends the
// window a changesTeam when what it holds has changed.
func (s *Server) runChangesWatch(ctx context.Context, c *controlClient, w *changesWatcher, root, req string) {
	var last []byte
	rev := 0
	tick := time.NewTicker(changesPoll)
	defer tick.Stop()
	for {
		t, ok := ask(s, func() changesTarget { return s.changesTargetOf(root) })
		if !ok || ctx.Err() != nil {
			return
		}
		if t.err != nil {
			// The project is gone: tell the window once and stop.
			changesRefuse(c, command{Cmd: "changesWatch", Req: req}, "noBranch", t.err.Error())
			s.do(func() {
				if c.changes != nil && c.changes.root == root {
					c.stopChangesWatch()
				}
			})
			return
		}
		rctx, cancel := context.WithTimeout(ctx, changesReadLimit)
		team := changes.ReadTeam(rctx, t.root, t.project, t.panes, 0)
		cancel()
		if ctx.Err() != nil {
			return
		}
		w.last.Store(&team)
		// What is compared leaves out the push's number and token.
		if body, err := json.Marshal(changesTeamMsg{Type: "changesTeam", Team: team}); err == nil && string(body) != string(last) {
			last = body
			rev++
			team.Rev, team.Req = rev, req
			if data, err := json.Marshal(changesTeamMsg{Type: "changesTeam", Team: team}); err == nil {
				c.send(data)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// changesFilesPage answers with one page of a branch's whole file list.
func (s *Server) changesFilesPage(c *controlClient, cmd command) {
	if cmd.ID == "" {
		changesRefuse(c, cmd, "badRequest", "id is required")
		return
	}
	limit := cmd.Limit
	if limit == 0 {
		limit = filesPageDefault
	}
	if cmd.Offset < 0 || limit < 1 || limit > filesPageMax {
		changesRefuse(c, cmd, "badRequest", "offset or limit is out of range")
		return
	}
	if !c.beginRead(changesBusyPage) {
		changesRefuse(c, cmd, "busy", changesBusyText)
		return
	}
	defer c.endRead(changesBusyPage)
	cwd, ok := s.changesPaneCwd(c, cmd)
	if !ok {
		return
	}
	// A window that is watching is served from the watch's last read, which is the
	// list its totals came from; one that is not has the checkout read for it.
	if w, _ := ask(s, func() *changesWatcher { return c.changes }); w != nil {
		if t := w.last.Load(); t != nil {
			for _, br := range t.Branches {
				if br.Pane == cmd.ID && br.Error == "" {
					all := br.AllFiles()
					c.sendJSON(changesFilesMsg{Type: "changesFiles", Files: changes.Files{Req: cmd.Req, Pane: cmd.ID, Offset: cmd.Offset,
						Total: len(all), Files: changes.Page(all, cmd.Offset, limit)}})
					return
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	b := changes.Read(ctx, cmd.ID, cwd, 0)
	if b.Error != "" {
		changesRefuse(c, cmd, "noBranch", b.Error)
		return
	}
	all := b.AllFiles()
	c.sendJSON(changesFilesMsg{Type: "changesFiles", Files: changes.Files{Req: cmd.Req, Pane: cmd.ID, Offset: cmd.Offset,
		Total: len(all), Files: changes.Page(all, cmd.Offset, limit)}})
}

// changesPaneCwd finds the checkout of the agent pane a command is about, or
// answers noBranch and says so.
func (s *Server) changesPaneCwd(c *controlClient, cmd command) (string, bool) {
	cwd, ok := ask(s, func() string {
		p := s.ws.Pane(cmd.ID)
		if p == nil {
			return ""
		}
		members, err := s.ws.TeamMembers(p.Root)
		if err != nil {
			return ""
		}
		for _, m := range members {
			if m.Pane == p.ID {
				return p.Cwd
			}
		}
		return ""
	})
	if !ok {
		return "", false
	}
	if cwd == "" {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return "", false
	}
	return cwd, true
}

// changesDiffHunks answers diffHunks.
func (s *Server) changesDiffHunks(c *controlClient, cmd command) {
	if !c.beginRead(changesBusyDiff) {
		changesRefuse(c, cmd, "busy", changesBusyText)
		return
	}
	defer c.endRead(changesBusyDiff)
	if cmd.ID == "" || cmd.File == "" {
		changesRefuse(c, cmd, "badRequest", "id and file are required")
		return
	}
	cwd, ok := s.changesPaneCwd(c, cmd)
	if !ok {
		return
	}
	n := changes.DefaultContext
	if cmd.Context != nil {
		n = *cmd.Context
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	d, ref := changes.Diff(ctx, cmd.ID, cwd, changes.DiffRequest{File: cmd.File, Against: cmd.Against, Context: n, IgnoreWhitespace: cmd.IgnoreWhitespace})
	if ref != nil {
		changesRefuse(c, cmd, ref.Code, ref.Msg)
		return
	}
	d.Req = cmd.Req
	c.sendJSON(diffHunksMsg{Type: "diffHunks", DiffHunks: d})
}

// changesDiffContext answers diffContext.
func (s *Server) changesDiffContext(c *controlClient, cmd command) {
	if !c.beginRead(changesBusyContext) {
		changesRefuse(c, cmd, "busy", changesBusyText)
		return
	}
	defer c.endRead(changesBusyContext)
	if cmd.ID == "" || cmd.File == "" {
		changesRefuse(c, cmd, "badRequest", "id and file are required")
		return
	}
	cwd, ok := s.changesPaneCwd(c, cmd)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	d, ref := changes.ReadContext(ctx, cmd.ID, cwd, cmd.File, cmd.Against, cmd.Side, cmd.From, cmd.To)
	if ref != nil {
		changesRefuse(c, cmd, ref.Code, ref.Msg)
		return
	}
	d.Req = cmd.Req
	c.sendJSON(diffContextMsg{Type: "diffContext", DiffContext: d})
}
