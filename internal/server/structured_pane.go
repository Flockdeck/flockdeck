package server

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/structured"
)

// runtimePane is a structured pane's runtime as permissionAnswer needs it.
type runtimePane struct{ rt *structured.Runtime }

func (p runtimePane) PendingPermission() *bridge.Card { return p.rt.Snapshot().Pending }

// AnswerPermission gives the choice to the runtime, which checks the card and
// takes the answer under one lock.
func (p runtimePane) AnswerPermission(seq uint64, requestID, optionID string) error {
	err := p.rt.AnswerCard(seq, requestID, optionID)
	if errors.Is(err, structured.ErrStale) {
		return ErrPermissionStale
	}
	return err
}

// workspaceStructuredPane is how a pane id becomes a structured pane for
// permissionAnswer: the workspace's pane of that id, if it is one.
func (s *Server) workspaceStructuredPane(id string) (StructuredPane, bool) {
	if rt := s.ws.StructuredRuntime(id); rt != nil {
		return runtimePane{rt}, true
	}
	return nil, false
}
