package server

import (
	"path/filepath"
	"sort"
	"time"
)

// agentView is one pane in the overview, across every open project.
type agentView struct {
	PaneID  string `json:"paneId"`
	TabID   string `json:"tabId"`
	Tab     string `json:"tab"`
	Root    string `json:"root"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Branch  string `json:"branch"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	For     string `json:"for"`
	Dirty   int    `json:"dirty"`
	Active  bool   `json:"active"`
}

type agentsMsg struct {
	Type  string      `json:"type"`
	Items []agentView `json:"items"`
}

// listAgents answers a request for every pane in every open project.
//
// The tab bar only shows the active project, so once several are open this is
// the only place that answers "where is the agent that needs me".
func (s *Server) listAgents(c *controlClient) {
	s.do(func() {
		msg := agentsMsg{Type: "agents"}
		focused := ""
		if t := s.ws.CurrentTab(); t != nil {
			focused = t.Focus
		}

		for _, t := range s.ws.Tabs {
			for _, id := range t.Tree.Panes() {
				p := s.ws.Pane(id)
				if p == nil {
					continue
				}
				st, detail := p.Status()
				av := agentView{
					PaneID:  p.ID,
					TabID:   t.ID,
					Tab:     t.Title,
					Root:    t.Root,
					Project: filepath.Base(t.Root),
					Name:    p.Name,
					Branch:  p.Branch,
					Kind:    kindName(p.Kind),
					Status:  st.String(),
					Detail:  detail,
					Dirty:   p.Git.Dirty + p.Git.Untracked,
					Active:  p.ID == focused,
				}
				if p.Sess != nil {
					av.For = humanAgo(time.Since(p.Sess.StatusSince()))
				}
				msg.Items = append(msg.Items, av)
			}
		}

		// Whatever needs a person comes first; that is the point of the list.
		rank := map[string]int{"waiting": 0, "working": 1, "idle": 2, "starting": 3, "exited": 4}
		sort.SliceStable(msg.Items, func(i, j int) bool {
			ri, rj := rank[msg.Items[i].Status], rank[msg.Items[j].Status]
			if ri != rj {
				return ri < rj
			}
			return msg.Items[i].Project < msg.Items[j].Project
		})
		c.sendJSON(msg)
	})
}

// revealPane brings a pane into view wherever it lives, switching project and
// tab as needed.
func (s *Server) revealPane(root, tabID, paneID string) {
	s.do(func() {
		if root != "" {
			s.ws.SelectProject(root)
		}
		if tabID != "" {
			s.ws.SelectTab(tabID)
		}
		s.ws.FocusPane(paneID)
		s.Wake()
	})
}
