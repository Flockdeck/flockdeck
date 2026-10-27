package server

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/mdexport"
)

// structuredExport saves the conversation of a structured pane as a Markdown file
// in a folder the person named (cmd.Dir). See internal/mdexport for what the file
// holds. The name of the file is made here and is never taken from the window; an
// existing file is never replaced. The text goes through the redactor first, the
// one the baton uses (the pane's secret environment values, the keys Flockdeck
// holds, the checkout's .env files and the shapes of keys), and a pane whose
// redactor cannot be made is not exported. cmd.Thinking adds the agent's
// thinking where the runtime still holds it.
//
// It is for a window on this machine (structuredCommand refuses the relay) and
// the answer is a teamResult for the op. Gathering runs on the workspace
// goroutine; reading the key store, redacting and writing do not.

const (
	structuredExportNoPane  = "That pane is gone"
	structuredExportBusy    = "An export of this pane is already running"
	structuredExportNoScrub = "The conversation was not exported because the redactor could not be set up"
	structuredExportFailed  = "The conversation could not be exported"
	// structuredExportBudget bounds the redacting of one export.
	structuredExportBudget = 60 * time.Second
)

// structuredExporting holds the panes with an export under way.
var structuredExporting sync.Map

func (s *Server) structuredExport(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	entries, ok := s.ws.StructuredEntries(cmd.ID)
	if !ok {
		refuse(c, cmd, errStructured(structuredExportNoPane))
		return
	}
	src, err := s.ws.BatonSource(cmd.ID)
	if err != nil {
		refuse(c, cmd, errStructured(structuredExportNoScrub))
		return
	}
	if _, running := structuredExporting.LoadOrStore(cmd.ID, true); running {
		refuse(c, cmd, errStructured(structuredExportBusy))
		return
	}
	title := src.Pane.Name
	agentID := src.Pane.Agent
	id, dir, req, thinking := cmd.ID, cmd.Dir, cmd.Req, cmd.Thinking
	go func() {
		defer structuredExporting.Delete(id)
		defer s.surviveFor(c, "exporting a conversation")
		sc := src.Scrubber().WithDeadline(time.Now().Add(structuredExportBudget))
		opts := mdexport.Options{
			Title:    title,
			Agent:    agentID,
			Exported: time.Now().UTC().Format(time.RFC3339),
			Thinking: thinking,
			Scrub: func(t string) (string, int) {
				out, reds := sc.Scrub(baton.CleanText(t))
				return out, baton.RedactionCount(reds)
			},
		}
		if thinking {
			opts.ThinkingText = func(eid string) (string, bool) {
				d, ok := rt.Detail(eid)
				return d.Text, ok
			}
		}
		res, err := mdexport.Render(entries, opts)
		var path string
		if err == nil {
			path, err = mdexport.WriteNew(dir, title+"-"+time.Now().Format("20060102-150405"), []byte(res.Markdown))
		}
		if err != nil {
			refuse(c, cmd, errStructured(exportWords(err)))
			return
		}
		m := teamResultMsg{Type: "teamResult", Req: req, Op: "structuredExport", OK: true, Path: path, Entries: res.Entries, Redacted: res.Redactions}
		if res.Cut {
			m.Warning = "The file was cut at the size limit and later entries are left out"
		}
		c.sendJSON(m)
	}()
}

// exportWords is the fixed text for a failed export.
func exportWords(err error) string {
	for _, known := range []error{mdexport.ErrNoDir, mdexport.ErrBadDir, mdexport.ErrTooBig, mdexport.ErrNoCreate} {
		if errors.Is(err, known) {
			w := known.Error()
			return strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return structuredExportFailed
}
