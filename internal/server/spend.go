package server

import (
	"time"

	"github.com/jmwri/flockdeck/internal/spend"
)

// installUsageHandler takes what the panes' agents say they have spent -- a
// chat pane after every call, a Claude pane on every refresh of its status
// line -- into the book the pane headers are drawn from.
//
// The book keeps its own lock, so a report is recorded where it arrives, on
// the hook server's goroutine, and the windows are woken to show it rather
// than the workspace goroutine being asked to do anything.
func (s *Server) installUsageHandler() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	hookSrv.SetUsageHandler(func(r spend.Report) {
		s.book.Add(r, time.Now())
		s.Wake()
	})
}

// spendAt is the moment a snapshot's spend is read as of, which is when a
// window that has reset stops being shown. Panes that have closed are dropped
// from the book on the way. It runs on the workspace goroutine.
func (s *Server) spendAt() time.Time {
	s.book.Retain(func(id string) bool { return s.ws.Pane(id) != nil })
	return time.Now()
}
