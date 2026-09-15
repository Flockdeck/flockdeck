package server

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/keybindings"
)

// setKeybinding gives one action a binding of its own, or -- for text == ""
// -- takes its binding away, reachable from the command palette alone. It
// runs off the workspace goroutine: keybindings.json is its own concern, read
// and written by the keybindings package's own lock, and never touches
// s.ws or s.prefs.
func (s *Server) setKeybinding(c *controlClient, id, text string) {
	if id == "" {
		return
	}
	if _, err := keybindings.SetBinding(id, text); err != nil {
		var conflict *keybindings.ConflictError
		if errors.As(err, &conflict) {
			c.notify(text+" is already "+conflict.With.Name(), true)
			return
		}
		c.notify("could not save the binding: "+err.Error(), true)
		return
	}
	s.broadcastKeys()
}

// resetKeybinding takes one action back to its built-in binding.
func (s *Server) resetKeybinding(c *controlClient, id string) {
	if id == "" {
		return
	}
	if _, err := keybindings.ResetBinding(id); err != nil {
		c.notify("could not reset the binding: "+err.Error(), true)
		return
	}
	s.broadcastKeys()
}

// resetKeybindings takes every action back to its built-in binding.
func (s *Server) resetKeybindings(c *controlClient) {
	if _, err := keybindings.ResetAll(); err != nil {
		c.notify("could not reset the bindings: "+err.Error(), true)
		return
	}
	s.broadcastKeys()
}
