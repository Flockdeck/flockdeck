package workspace

import (
	"fmt"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

// PromotePane gives a running agent that has no role the role, which is how the
// person makes an agent they started by hand the manager (or a worker, or any
// other role) of a team. It runs on the workspace goroutine.
//
// Only an agent that has no role and was not started by another agent is
// promoted: a helper has the role it was started for, and a role is not taken
// from an agent that has one. A pane that has ended, and a shell, have no role to
// take. At most one manager may be running in a project, so promoting a second
// is refused.
//
// The agent is told in its inbox, as a notice from Flockdeck, that it has the
// role now and where to read what that means; its briefing was written before it
// had one and is not sent again.
func (w *Workspace) PromotePane(id, role string) error {
	p := w.Pane(id)
	if p == nil {
		return fmt.Errorf("that pane is not open any more")
	}
	if !p.IsAgent() {
		return fmt.Errorf("a shell has no role; only an agent can be made %s", role)
	}
	if stat, _ := w.Runtime(p).Status(); stat == session.StatusExited {
		return fmt.Errorf("that agent has ended, so it cannot be made %s", role)
	}
	if p.Role != "" {
		return fmt.Errorf("that agent is already the %s; a role is not changed once an agent has one", p.Role)
	}
	if p.Parent != "" {
		return fmt.Errorf("that agent was started by another agent for its own job; only an agent started by hand can be promoted")
	}
	if _, err := w.agents().CheckRole(role); err != nil {
		return err
	}
	if role == team.ManagerRole {
		for _, q := range w.projectPanes(w.rootOf(id), id, true) {
			if q.ID != id && q.Role == team.ManagerRole {
				if st, _ := w.Runtime(q).Status(); st != session.StatusExited {
					return fmt.Errorf("this project already has a manager running (%s); close it first or keep the team it leads", q.Name)
				}
			}
		}
	}
	w.mu.Lock()
	p.Role = role
	w.mu.Unlock()
	w.team.inbox.SendSystem(id, "The person has made you the "+role+" of this project's team. Run `flockdeck team` to see the team and the shape of the work, and `flockdeck task list` for the board.")
	w.wake()
	return nil
}
