package server

import (
	"fmt"
	"math"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
)

// A team spend limit is a number of dollars per project, kept in the
// preferences (store.Prefs.SpendLimits) and set with the teamSpendLimit command.
// It never stops an agent that is running: at 80 percent of it the team's needs
// list says so (team.NeedSpend), and once it is spent a start of another helper
// is refused with a reply that says why and how to go on.

// setTeamSpendLimit is the teamSpendLimit command: cmd.Root names the project and
// cmd.Limit the dollars, zero for none.
func (s *Server) setTeamSpendLimit(c *controlClient, cmd command) {
	if math.IsNaN(cmd.Limit) || cmd.Limit < 0 || cmd.Limit > store.MaxSpendLimit {
		refuse(c, cmd, fmt.Errorf("a spend limit is between 0 and $%d, and 0 means none", store.MaxSpendLimit))
		return
	}
	root, err := s.ws.TeamRoot(cmd.Root)
	if err != nil {
		refuse(c, cmd, err)
		return
	}
	s.updatePrefs(c, func(p *store.Prefs) bool { return p.SetSpendLimit(root, cmd.Limit) })
	// The graph carries the limit, so the windows watching it are given the new one.
	s.Wake()
}

// spendRoom says whether the agent in the pane parent may start another helper
// as far as its team's spend limit goes: nil while the team is under the limit
// or has none. It runs on the workspace goroutine, before a worktree is cut.
func (s *Server) spendRoom(parent string) error {
	p := s.ws.Pane(parent)
	if parent == "" || p == nil {
		return nil
	}
	root := s.ws.RootOf(parent)
	limit := s.prefs.SpendLimitFor(root)
	if limit <= 0 {
		return nil
	}
	g, err := s.graphPayload(root)
	if err != nil {
		// A project that cannot be read has no spend to count; the start goes on.
		return nil
	}
	for _, tm := range g.Teams {
		for _, id := range tm.Members {
			if id != parent {
				continue
			}
			if team.SpendLevel(tm.SpendUSD, limit) == "over" {
				floor := ""
				if tm.SpendPartial {
					floor = "at least "
				}
				return fmt.Errorf("this team has spent %s$%.2f of its $%.2f limit, so no new helper starts. Agents already running are not stopped. Ask the user to raise or clear the limit", floor, tm.SpendUSD, limit)
			}
		}
	}
	return nil
}
