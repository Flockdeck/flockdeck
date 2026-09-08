package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/agent-wrapper/internal/gitx"
	"github.com/jmwri/agent-wrapper/internal/hooks"
	"github.com/jmwri/agent-wrapper/internal/session"
	"github.com/jmwri/agent-wrapper/internal/workspace"
)

// fanoutPreviewMsg offers the work found in a pane's output for the user to
// edit before anything is started.
type fanoutPreviewMsg struct {
	Type   string   `json:"type"`
	PaneID string   `json:"paneId"`
	Cwd    string   `json:"cwd"`
	IsRepo bool     `json:"isRepo"`
	Tasks  []string `json:"tasks"`
	// FromReply reports whether the tasks were read from what the agent said,
	// rather than scraped off the pane's screen.
	FromReply bool `json:"fromReply"`
	// Trusted reports whether this directory has already been trusted in
	// Claude Code, which is what makes it meaningful to offer the same for the
	// worktrees a fan-out creates.
	Trusted bool   `json:"trusted"`
	Project string `json:"project"`
}

// previewFanout reads a pane's recent output and proposes tasks from it.
func (s *Server) previewFanout(c *controlClient, paneID string) {
	type info struct {
		id  string
		cwd string
		src workspace.PlanSource
	}
	done := make(chan info, 1)
	s.do(func() {
		id := paneID
		if id == "" {
			if t := s.ws.CurrentTab(); t != nil {
				id = t.Focus
			}
		}
		cwd := s.ws.ActiveRoot()
		if p := s.ws.Pane(id); p != nil {
			cwd = p.Cwd
		}
		done <- info{id: id, cwd: cwd, src: s.ws.PlanSourceFor(id)}
	})
	// s.do drops the callback once the server is closing, so every reply from
	// the workspace goroutine has to be waited for with a way out. Without one
	// the receive never returns and takes its caller down with it.
	var in info
	select {
	case in = <-done:
	case <-s.closed:
		return
	}

	go func() {
		// Reading the transcript touches the disk, which is why it happens here
		// rather than on the goroutine that owns the workspace.
		tasks, fromReply := in.src.Tasks()
		msg := fanoutPreviewMsg{
			Type:      "fanoutPreview",
			PaneID:    in.id,
			Cwd:       in.cwd,
			Tasks:     tasks,
			FromReply: fromReply,
			IsRepo:    isRepoDir(in.cwd) || gitRoot(in.cwd) != "",
			Trusted:   session.IsTrusted(in.cwd),
			Project:   filepath.Base(in.cwd),
		}
		c.sendJSON(msg)
	}()
}

// gitRoot reports the repository containing dir, if any.
func gitRoot(dir string) string {
	if !gitx.Available() {
		return ""
	}
	root, err := gitx.Root(dir)
	if err != nil {
		return ""
	}
	return root
}

// runFanout starts one agent per task.
//
// Worktrees are created before anything touches the workspace, because git is
// slow and the workspace goroutine also serves every window's state.
func (s *Server) runFanout(c *controlClient, parent string, tasks []string, worktrees, split, trust bool) {
	go func() {
		if len(tasks) == 0 {
			c.notify("no tasks to start", true)
			return
		}

		// Resolve the parent's directory once, off the workspace goroutine.
		done := make(chan string, 1)
		s.do(func() {
			id := parent
			if id == "" {
				if t := s.ws.CurrentTab(); t != nil {
					id = t.Focus
				}
			}
			cwd := s.ws.ActiveRoot()
			if p := s.ws.Pane(id); p != nil {
				cwd = p.Cwd
			}
			parent = id
			done <- cwd
		})
		var baseCwd string
		select {
		case baseCwd = <-done:
		case <-s.closed:
			return
		}

		// Branch names are derived from the task text and then truncated, so two
		// tasks that begin alike would otherwise land on the same branch — and
		// PrepareWorktree reuses an existing worktree, which would quietly put
		// two agents in one checkout. Keep them distinct.
		repo := gitRoot(baseCwd)
		used := map[string]bool{}

		started, failed := 0, 0
		for _, task := range tasks {
			task = strings.TrimSpace(task)
			if task == "" {
				continue
			}
			// Every task here becomes an agent with a terminal of its own. The
			// proposed list is capped, but the user edits it before anything
			// starts, and nothing between there and here held the edited one to
			// a size the machine can actually run.
			if started+failed >= workspace.MaxTasks {
				c.notify(fmt.Sprintf("stopped after %d agents; start the rest as a second fan-out", workspace.MaxTasks), true)
				break
			}

			cwd := baseCwd
			if worktrees {
				branch := uniqueBranch(repo, workspace.BranchNameFor(task), used)
				used[branch] = true
				path, err := s.ws.PrepareWorktree(baseCwd, branch)
				if err != nil {
					c.notify(fmt.Sprintf("%s: %v", short(task), err), true)
					failed++
					continue
				}
				cwd = path

				// A brand new worktree is a directory Claude has not seen, so
				// it would stop and ask whether the folder is trusted before
				// doing anything. Carrying over the answer already given for
				// the project it was cut from is what the user asked for by
				// ticking the box.
				if trust {
					if err := session.InheritTrust(baseCwd, cwd); err != nil {
						c.notify("could not carry over folder trust: "+err.Error(), true)
						trust = false
					}
				}
			}

			res := make(chan error, 1)
			s.do(func() {
				_, err := s.ws.Spawn(parent, workspace.SpawnOptions{
					Task:  task,
					Cwd:   cwd,
					Split: split,
					Kind:  session.KindClaude,
				})
				res <- err
			})
			var err error
			select {
			case err = <-res:
			case <-s.closed:
				return
			}
			if err != nil {
				c.notify(fmt.Sprintf("%s: %v", short(task), err), true)
				failed++
				continue
			}
			started++
		}

		if started > 0 {
			word := "agents"
			if started == 1 {
				word = "agent"
			}
			// Each failure has already been reported on its own, but a fan-out
			// opens a screenful of panes: without the count in the summary, a
			// task that never started reads as one the user simply lost track
			// of among the ones that did.
			if failed > 0 {
				c.notify(fmt.Sprintf("started %d %s, %d could not be started", started, word, failed), true)
			} else {
				c.notify(fmt.Sprintf("started %d %s", started, word), false)
			}
		}
		s.Wake()
	}()
}

// contextDeadline bounds how long a pane's SessionStart hook waits for its
// description. It is shorter than the hook's own timeout so the answer, when
// there is one, always arrives in time to be used.
const contextDeadline = 2 * time.Second

// installContextHandler answers a pane's SessionStart hook with a description
// of the pane its agent is running in.
//
// The reply is built on the goroutine that owns the workspace, like every other
// read of it. Claude Code is waiting on this hook, so the work done there is
// only a walk over state already in memory.
func (s *Server) installContextHandler() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	hookSrv.SetContextHandler(func(paneID string) string {
		out := make(chan string, 1)
		s.do(func() {
			c, ok := s.ws.PaneContext(paneID)
			if !ok {
				out <- ""
				return
			}
			out <- c.Render()
		})
		// Claude Code is held up until this hook answers, so a busy or wedged
		// workspace loop must not be able to stall a pane's startup: give up
		// and let the agent begin without knowing where it is.
		select {
		case text := <-out:
			return text
		case <-time.After(contextDeadline):
			return ""
		case <-s.closed:
			return ""
		}
	})
}

// errShuttingDown is what a spawn is answered with when the window it would
// have opened in is already going away.
var errShuttingDown = errors.New("the workspace is shutting down")

// installSpawnHandler lets an agent start helpers of its own by running
// `agent-wrapper spawn` inside its pane.
func (s *Server) installSpawnHandler() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	hookSrv.SetSpawnHandler(func(req hooks.SpawnRequest) (string, error) {
		// Work out where the child should run before touching the workspace.
		done := make(chan string, 1)
		s.do(func() {
			cwd := s.ws.ActiveRoot()
			if p := s.ws.Pane(req.Parent); p != nil {
				cwd = p.Cwd
			}
			done <- cwd
		})
		// The agent's `agent-wrapper spawn` is blocked on this reply, so a
		// closing workspace has to answer it rather than leave the command
		// hanging in the pane forever.
		var cwd string
		select {
		case cwd = <-done:
		case <-s.closed:
			return "", errShuttingDown
		}

		if req.Branch != "" {
			path, err := s.ws.PrepareWorktree(cwd, req.Branch)
			if err != nil {
				return "", err
			}
			cwd = path
		}

		kind := session.KindClaude
		if req.Shell {
			kind = session.KindShell
		}

		type result struct {
			id  string
			err error
		}
		res := make(chan result, 1)
		s.do(func() {
			id, err := s.ws.Spawn(req.Parent, workspace.SpawnOptions{
				Task:  req.Task,
				Cwd:   cwd,
				Split: req.Split,
				Kind:  kind,
			})
			res <- result{id, err}
		})
		var r result
		select {
		case r = <-res:
		case <-s.closed:
			return "", errShuttingDown
		}
		if r.err == nil {
			s.Wake()
		}
		return r.id, r.err
	})
}

// uniqueBranch returns base, or base-2, base-3 and so on, until it names a
// branch that neither exists nor has already been handed out in this fan-out.
func uniqueBranch(repo, base string, used map[string]bool) string {
	candidate := base
	for i := 2; ; i++ {
		free := !used[candidate]
		if free && repo != "" && gitx.BranchExists(repo, candidate) {
			free = false
		}
		if free {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

// short trims a task for a one-line message.
func short(task string) string {
	if r := []rune(task); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return task
}
