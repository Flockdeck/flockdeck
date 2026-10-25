package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jmwri/flockdeck/internal/changes"
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

// changesWatcher is a window's watch: stopping it ends its goroutine.
type changesWatcher struct {
	root   string
	cancel context.CancelFunc
}

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
	for _, p := range panes {
		if agents[p.ID] && p.Cwd != "" {
			t.panes = append(t.panes, changes.PaneRef{ID: p.ID, Cwd: p.Cwd})
		}
	}
	return t
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
	}
}

func changesRefuse(c *controlClient, cmd command, code, msg string) {
	c.sendJSON(changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, Code: code, Error: msg})
}

func (s *Server) changesSnapshot(c *controlClient, cmd command) {
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
		c.changes = &changesWatcher{root: t.root, cancel: cancel}
		go s.runChangesWatch(ctx, c, t.root, cmd.Req)
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
func (s *Server) runChangesWatch(ctx context.Context, c *controlClient, root, req string) {
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
		return
	}
	if cwd == "" {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return
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
