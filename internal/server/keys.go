package server

import (
	"slices"
	"sync"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
)

// keysMsg answers the API keys dialog: every agent that talks to a model API,
// whether it has a key and where that key came from. Never the key itself --
// creds.Status has nowhere to put one.
type keysMsg struct {
	Type  string         `json:"type"`
	Items []creds.Status `json:"items"`
}

// keyWrites keeps two saves from the dialog from reading the store at the same
// moment and each writing back a copy without the other's key.
var keyWrites sync.Mutex

// listKeys answers the dialog. The catalog is read on the workspace goroutine,
// like every other read of it; the key store is a file, and is read off it.
func (s *Server) listKeys(c *controlClient) {
	specs, ok := ask(s, func() []agent.Spec {
		specs, _ := s.ws.Agents()
		return specs
	})
	if !ok {
		return
	}
	go func() {
		defer s.survive("reading the API keys")
		visible := slices.DeleteFunc(slices.Clone(specs), func(sp agent.Spec) bool { return sp.Hidden })
		items := creds.StatusAll(visible)
		// A key stored for an agent that has since left the catalog has no row
		// of its own, and a key nobody can see is a key nobody can clear.
		// A store that cannot be read makes every stored key read as not set,
		// which on the dialog is indistinguishable from never having set one --
		// until a save is refused for the same reason. It is said instead.
		names, err := creds.Names()
		if err != nil {
			c.notify("could not read the stored keys: "+err.Error(), true)
		}
		for _, id := range names {
			if !slices.ContainsFunc(items, func(st creds.Status) bool { return st.Agent == id }) {
				items = append(items, creds.Status{Agent: id, Set: true, Source: creds.SourceStore})
			}
		}
		c.sendJSON(keysMsg{Type: "keys", Items: items})
	}()
}

// setKey stores a key typed into the dialog. What goes back says only that
// there is one: the notice names the agent, and the dialog is sent its list
// again.
func (s *Server) setKey(c *controlClient, agentID, key string) {
	go func() {
		defer s.survive("saving a key")
		keyWrites.Lock()
		err := creds.Set(agentID, key)
		keyWrites.Unlock()
		if err != nil {
			c.notify("could not save the key: "+err.Error(), true)
		} else {
			c.notify(s.savedKeyNotice(agentID), false)
		}
		s.keysChanged(c)
	}()
}

// savedKeyNotice says that a key was saved, and whether it is the one used.
//
// A key already in flockdeck's environment comes first (creds.Resolve), so one
// saved over it is kept for later and does nothing while the variable is set.
// The dialog offers Replace… for both kinds alike, and "saved" on its own read
// as though the key just typed was now in use.
func (s *Server) savedKeyNotice(agentID string) string {
	text := "saved the key for " + agentID
	if spec, ok := s.ws.Catalog().Find(agentID); ok {
		if st := creds.StatusOf(spec); st.Source == creds.SourceEnv {
			text += ", but " + st.Env + " in flockdeck's environment is what is used while it is set"
		}
	}
	return text
}

// clearKey forgets a stored key.
func (s *Server) clearKey(c *controlClient, agentID string) {
	go func() {
		defer s.survive("clearing a key")
		keyWrites.Lock()
		_, err := creds.Clear(agentID)
		keyWrites.Unlock()
		if err != nil {
			c.notify("could not clear the key: "+err.Error(), true)
		} else {
			c.notify("cleared the key for "+agentID, false)
		}
		s.keysChanged(c)
	}()
}

// keysChanged redraws the dialog and has the machine asked about the agents
// again: an API agent can be started exactly when it has a key, so the picker
// should stop greying it out the moment it has one rather than when the
// cached answer happens to go stale.
func (s *Server) keysChanged(c *controlClient) {
	s.listKeys(c)
	s.refreshAgents()
}
