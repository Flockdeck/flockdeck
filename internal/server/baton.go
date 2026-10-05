package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file is the server side of handing one agent's work to another: the
// window's commands for making, saving and starting from a baton, and the
// resolver `flockdeck spawn --baton` goes through. The document itself is
// internal/baton; what to do with a pane is internal/workspace.

// batonDraftMsg answers makeBaton with the draft to edit.
type batonDraftMsg struct {
	Type   string `json:"type"`
	PaneID string `json:"paneId"`
	// Req is the id the window gave the makeBaton this answers, so the draft is
	// matched to the dialog that asked and not to a pane alone.
	Req string `json:"req,omitempty"`
	// Name is the pane's name, and Agent the agent id it runs.
	Name  string `json:"name"`
	Agent string `json:"agent"`
	// Provider is who the source agent sends its conversation to, and Agents
	// the catalog the target is chosen from, each with its own Provider, so
	// the window can say when handing the work to another company.
	Provider string           `json:"provider"`
	Agents   []batonAgentView `json:"agents,omitempty"`
	Model    string           `json:"model,omitempty"`
	Text     string           `json:"text"`
	Notes    []string         `json:"notes,omitempty"`
	// Dirty says the source checkout has uncommitted work, which a new
	// worktree does not have; IsRepo says there is a checkout to cut one from.
	Dirty    bool `json:"dirty"`
	IsRepo   bool `json:"isRepo"`
	Scrubbed int  `json:"scrubbed"`
	// MarkKinds are the kinds of mark the scrubber makes, so the window counts
	// the marks in the text the way the server does.
	MarkKinds []string `json:"markKinds,omitempty"`
}

// batonAgentView is an agent the baton can go to.
type batonAgentView struct {
	fanoutAgentView
	Provider string `json:"provider,omitempty"`
}

// batonSavedMsg answers saveBaton, startFromBaton and restartWithBaton once
// they have worked: which baton was kept, where, and how many values the last
// scrub removed. It carries no text; what was kept is in the file at Path.
type batonSavedMsg struct {
	Type   string `json:"type"`
	PaneID string `json:"paneId"`
	// Req is the id the window gave the command this answers.
	Req string `json:"req,omitempty"`
	// Cmd is the command this answers, so a late answer to an earlier one is
	// not taken for the answer to the one now out.
	Cmd      string `json:"cmd"`
	ID       string `json:"id"`
	Path     string `json:"path"`
	Scrubbed int    `json:"scrubbed"`
	// Started is the pane the baton was started in, for the two commands that
	// start one, and empty for saveBaton.
	Started string `json:"started,omitempty"`
}

// batonErrorMsg answers a baton command that was refused or failed, so the
// window can tell its own request failing from any other notice arriving while
// it waits. The notice that says why is sent as well.
type batonErrorMsg struct {
	Type   string `json:"type"`
	PaneID string `json:"paneId"`
	// Req is the id the window gave the command that failed. The window matches
	// the answer to the command by it, so an error for another pane's command, or
	// a late one for an earlier command, cannot be taken for this one's.
	Req   string `json:"req,omitempty"`
	Cmd   string `json:"cmd"`
	Error string `json:"error"`
}

// batonFail refuses a baton command: the notice for the person, and the answer
// the baton dialog is waiting for.
func (s *Server) batonFail(c *controlClient, paneID, cmd, req string, err error) {
	c.notify(err.Error(), true)
	c.sendJSON(batonErrorMsg{Type: "batonError", PaneID: paneID, Req: req, Cmd: cmd, Error: err.Error()})
}

// batonSurvive is deferred by work done for a baton command. A panic is reported
// like any other, and the window is also told its command failed, so a dialog is
// not left waiting for an answer that will never come.
func (s *Server) batonSurvive(c *controlClient, paneID, cmd, req, doing string) {
	if r := recover(); r != nil {
		s.reportPanic(c, doing, r)
		c.sendJSON(batonErrorMsg{Type: "batonError", PaneID: paneID, Req: req, Cmd: cmd,
			Error: "Flockdeck hit an internal error " + doing})
	}
}

// batonPanicHook, when set, is called at the start of the work a baton command
// does off the dispatch goroutine. A test sets it to panic.
var batonPanicHook func()

// providerChange says what stands in the way of handing a baton from one agent
// to another without asking: the two are not the same agent, and either they
// belong to different companies or one company is not known. It is empty when
// the baton stays with the same agent or the same company.
func providerChange(fromAgent, fromProvider, toAgent, toProvider string) string {
	if fromAgent != "" && fromAgent == toAgent {
		return ""
	}
	switch {
	case fromProvider == "" || toProvider == "":
		return "the company of one of the two agents is not known"
	case fromProvider != toProvider:
		return "it would go from " + shownProvider(fromProvider) + " to " + shownProvider(toProvider)
	}
	return ""
}

// handoff is a baton resolved from an agent's reference, with what the
// application knows about it that the baton's own text cannot say. Source is the
// id of the agent it was made from, and "" when that is not known: it is known
// only for a baton made here from a pane (self or a pane id), or one the
// application stored and recorded the source of. A baton's header, a file's
// front matter, says "agent: claude" for anyone who writes it, so it is not
// read for this.
type handoff struct {
	baton.Baton
	Source string
	// Scrubber is the calling pane's, for the last scrub before the baton is
	// used.
	Scrubber *baton.Scrubber
}

// recordSource notes which agent a stored baton was made from, so that a later
// `spawn -baton <id>` can tell where it came from without trusting its header.
//
// A failure is written to the console and to error.log in the state directory,
// and returned, so a caller with a window can say so; it is not otherwise fatal.
// The baton is saved, and with no source recorded a later spawn from it is
// treated as having an unknown one, which needs -baton-send-elsewhere.
func recordSource(id, agentID string) error {
	if agentID == "" {
		return nil
	}
	st, err := baton.Open()
	if err == nil {
		err = st.SetSource(id, agentID)
	}
	if err != nil {
		logf("could not record that baton %s came from %s: %v", id, agentID, err)
	}
	return err
}

// sourceNotRecorded is what the window is told when a baton was saved but where it
// came from could not be recorded.
const sourceNotRecorded = "Saved the baton, but its source could not be recorded: sending it to another company will need -baton-send-elsewhere"

// logf reports something that went wrong and does not stop anything: to the
// console, and to error.log in the state directory, which a Flockdeck started
// from a shortcut, with no console, still has. It is a variable so that a test
// can see it.
var logf = func(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "flockdeck: %s\n", line)
	dir, err := store.Dir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "error.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), line)
	_ = f.Close()
}

// batonChange says how an agent starting another from a baton would send it to a
// different company, or to one that is not known, or is "" when it would not.
// The source is the one the application recorded, never the baton's header; where
// none is recorded (a notes file, a baton the application did not record) the
// company is not known. It must run on the workspace goroutine.
func (s *Server) batonChange(h *handoff, parent, cwd, source, branch, agentID, model string) (string, approvalInfo, *workspace.ApprovedTarget) {
	// The agent the helper will run, worked out as Spawn works it out: for the
	// project of the pane that spawns it, which is not the one on screen.
	resolved, _ := s.ws.SpawnTarget(parent, cwd, agentID, model)
	target, err := s.ws.AgentSpecByID(resolved)
	if err != nil {
		return "", approvalInfo{}, nil // Spawn says so itself
	}
	// The company of each is read for the folder the helper will work in, where
	// Claude Code's project settings are.
	fromProvider, fromWhy, fromName := "", "", ""
	if h.Source != "" {
		if sp, err := s.ws.AgentSpecByID(h.Source); err == nil {
			// Where the baton came from: the folder the source pane works in.
			fromProvider, fromWhy = workspace.BatonProviderDetail(sp, source)
			fromName = sp.Name
		}
	}
	// Where the helper will work: the worktree, with the settings of the checkout it is cut
	// from and, for a branch that exists, what the branch itself commits.
	var blobs [][]byte
	unknown := ""
	if branch != "" {
		blobs, unknown = s.ws.BranchSettings(source, branch)
	}
	toProvider, toWhy := workspace.BatonProviderWith(target, cwd, workspace.Sources{Dirs: alsoFrom(source, cwd), Settings: blobs, Unknown: unknown})
	change := providerChange(h.Source, fromProvider, target.ID, toProvider)
	if change == "" && h.Source != "" && fromProvider != toProvider {
		// The same agent, and where it works makes it go to another company: that is a
		// change of company all the same.
		change = "it would go from " + providerText(fromProvider, fromWhy) + " to " + providerText(toProvider, toWhy) + " in the folder it works in"
	}
	if change != "" && (h.Source == "" || fromProvider == "") {
		change = "where this baton came from is not known"
	}
	info := approvalInfo{
		Dest:   target.Name + " (" + providerText(toProvider, toWhy) + ")",
		Source: "has an unknown source",
		Asker:  "A helper pane",
	}
	if fromName != "" {
		info.Source = "came from " + fromName + " (" + providerText(fromProvider, fromWhy) + ")"
	}
	if p := s.ws.Pane(parent); p != nil {
		info.Asker = "The pane \"" + p.Name + "\""
	}
	return change, info, &workspace.ApprovedTarget{Agent: target.ID, Provider: toProvider, SourceProvider: fromProvider, From: alsoFrom(source, cwd)}
}

// alsoFrom is the checkout a worktree will be cut from, whose project settings the
// worktree will have, when the worktree is not the checkout itself.
func alsoFrom(source, cwd string) []string {
	if source == "" || filepath.Clean(source) == filepath.Clean(cwd) {
		return nil
	}
	return []string{source}
}

// shownProvider is a provider as it may be said to the user and the agent: an API
// agent's endpoint is not repeated, since a base URL can carry a credential.
func shownProvider(p string) string {
	if p == "" {
		return "a company that is not known"
	}
	if i := strings.Index(p, " via "); i >= 0 {
		return p[:i] + " through a custom endpoint"
	}
	return p
}

// providerText is what is said of an agent's company in a notice: the company, with
// the host only of an endpoint; or why it is not known, never empty brackets.
func providerText(provider, why string) string {
	if provider == "" {
		if why == "" {
			why = "company not known"
		}
		return why
	}
	return workspace.ProviderHost(provider)
}

// batonRefusal is what an agent is told when it spawns a helper from a baton that
// would go to another company and did not say it means to.
func batonRefusal(change string) string {
	return "this baton would be sent to a different company (" + change + "). It holds what an agent worked out; " +
		"add -baton-send-elsewhere to spawn to ask the user to allow it in the Flockdeck window"
}

// batonPaneID is the pane a command is about: the one it names, or else the one
// in focus, which is the only part that has to ask the workspace goroutine.
func (s *Server) batonPaneID(id string) (string, bool) {
	if id != "" {
		return id, true
	}
	return ask(s, func() string { return paneIDFor(s.ws, "") })
}

// batonAgent is the agent a new pane would run: the one named, which the catalog
// answers from any goroutine, or else the default, which depends on the project
// on screen and so is asked of the workspace goroutine. The bool is false when
// Flockdeck could not answer.
func (s *Server) batonAgent(id string) (agent.Spec, error, bool) {
	if id != "" {
		spec, err := s.ws.AgentSpecByID(id)
		return spec, err, true
	}
	type answer struct {
		spec agent.Spec
		err  error
	}
	a, ok := ask(s, func() answer {
		spec, err := s.ws.AgentSpec("")
		return answer{spec, err}
	})
	return a.spec, a.err, ok
}

// makeBaton drafts a baton from a pane and sends it to the window to edit.
// Reading the transcript and asking git take a moment, so they happen off the
// workspace goroutine, which only describes the pane.
func (s *Server) makeBaton(c *controlClient, paneID, req string) {
	defer s.batonSurvive(c, paneID, "makeBaton", req, "while making a baton")
	id, ok := s.batonPaneID(paneID)
	if !ok {
		s.batonFail(c, paneID, "makeBaton", req, errors.New("Flockdeck could not answer; try again"))
		return
	}
	// Describing the pane is a copy taken under the workspace's lock: nothing here
	// runs on the workspace goroutine.
	src, err := s.ws.BatonSource(id)
	if err != nil {
		s.batonFail(c, id, "makeBaton", req, err)
		return
	}
	in := struct {
		id  string
		src workspace.BatonSource
	}{id, src}
	go func() {
		defer s.batonSurvive(c, in.id, "makeBaton", req, "while making a baton")
		if batonPanicHook != nil {
			batonPanicHook()
		}
		b := in.src.Make(false)
		msg := batonDraftMsg{
			Type: "batonDraft", PaneID: in.id, Req: req,
			Name: in.src.Pane.Name, Agent: in.src.Spec.ID, Model: in.src.Pane.Model,
			Provider:  workspace.BatonProviderShown(in.src.Spec, in.src.Pane.Cwd),
			Text:      baton.Render(b),
			Dirty:     b.Section(baton.NotInCheckout) != "",
			IsRepo:    gitRoot(in.src.Pane.Cwd) != "",
			Scrubbed:  baton.RedactionCount(b.Redactions),
			MarkKinds: baton.MarkKinds(),
		}
		// The catalog has a lock of its own; asking which agents can start probes
		// PATH, which is why it is here and not on the workspace goroutine.
		for _, v := range s.fanoutCatalog(s.ws.Catalog().Visible()) {
			view := batonAgentView{fanoutAgentView: v}
			if spec, err := s.ws.AgentSpecByID(v.ID); err == nil {
				view.Provider = workspace.BatonProviderShown(spec, in.src.Pane.Cwd)
			}
			msg.Agents = append(msg.Agents, view)
		}
		if !in.src.Readable() {
			msg.Notes = append(msg.Notes, "This agent keeps no conversation Flockdeck can read. The baton has what git shows and the pane's task; write the goal, decisions and constraints yourself.")
		}
		c.sendJSON(msg)
	}()
}

// finishBaton turns the text the window sends back into the baton to keep. The
// text is what a person edited, so it is scrubbed again with everything the
// pane's own scrubber knows, and kept under a new id if the one it carries is
// already stored: a baton is not changed once saved.
func (s *Server) finishBaton(src workspace.BatonSource, text string) (baton.Baton, error) {
	b, err := baton.Parse(text)
	if err != nil {
		return baton.Baton{}, errors.New("the baton's header between the two --- lines is missing; put it back, or make the baton again")
	}
	b = src.Scrubber().ScrubBaton(b)
	st, err := baton.Open()
	if err != nil {
		return baton.Baton{}, err
	}
	if !baton.ValidID(b.ID) {
		b.ID = baton.NewID(time.Now())
	} else if _, err := st.Load(b.ID); err == nil {
		b = b.Fork(time.Now())
	}
	if b.Created.IsZero() {
		b.Created = time.Now()
	}
	return b, nil
}

// batonCommand runs saveBaton, startFromBaton and restartWithBaton, which
// differ only in what is done with the baton once it is final.
//
// Everything slow is done here, off the workspace goroutine: scrubbing with the
// pane's own secrets, saving, framing and writing the overflow file
// (workspace.PrepareBaton), and the worktree. The workspace is asked only to
// start or restart the pane with the finished prompt.
func (s *Server) batonCommand(c *controlClient, cmd command) {
	defer s.batonSurvive(c, cmd.ID, cmd.Cmd, cmd.Req, "while using a baton")
	type facts struct {
		id  string
		src workspace.BatonSource
		cwd string
		// target is the agent a new pane would run, and agentErr why it cannot.
		target   agent.Spec
		agentErr error
		err      error
	}
	id, ok := s.batonPaneID(cmd.ID)
	if !ok {
		s.batonFail(c, cmd.ID, cmd.Cmd, cmd.Req, errors.New("Flockdeck could not answer; check flockdeck baton list before trying again"))
		return
	}
	src, err := s.ws.BatonSource(id)
	in := facts{id: id, src: src, err: err}
	if err == nil {
		in.cwd = src.Pane.Cwd
		if cmd.Cmd == "startFromBaton" {
			var ok bool
			if in.target, in.agentErr, ok = s.batonAgent(cmd.Agent); !ok {
				s.batonFail(c, id, cmd.Cmd, cmd.Req, errors.New("Flockdeck could not answer; check flockdeck baton list before trying again"))
				return
			}
		}
	}
	fail := func(err error) { s.batonFail(c, in.id, cmd.Cmd, cmd.Req, err) }
	if in.err != nil {
		fail(in.err)
		return
	}
	if in.agentErr != nil {
		fail(in.agentErr)
		return
	}
	// A start that would send the baton to another company, or to one that is not
	// known, needs the person to say so. In a window on this machine that is the
	// tick the dialog asks for: a window that does not send it, or a command sent
	// by hand, is refused here as well. In a window reached through the relay the
	// tick is not enough, since it is only a field the phone fills in: the start
	// then waits for the approval notice in a window on this machine, the same one
	// `spawn -baton-send-elsewhere` waits for, and for nothing else.
	var approve *approvalInfo
	if cmd.Cmd == "startFromBaton" {
		from, fromWhy := workspace.BatonProviderDetail(in.src.Spec, in.src.Pane.Cwd)
		to, toWhy := workspace.BatonProviderDetail(in.target, in.src.Pane.Cwd)
		if change := providerChange(in.src.Spec.ID, from, in.target.ID, to); change != "" {
			switch {
			case c.remote:
				approve = &approvalInfo{
					Asker:  "A phone or other window reached through the relay",
					Dest:   in.target.Name + " (" + providerText(to, toWhy) + ")",
					Source: "came from " + in.src.Spec.Name + " (" + providerText(from, fromWhy) + ")",
				}
			case !cmd.Confirmed:
				fail(errors.New("this baton would be sent to a different company (" + change + "); tick Send it there to do it"))
				return
			}
		}
	}
	go func() {
		defer s.batonSurvive(c, in.id, cmd.Cmd, cmd.Req, "while using a baton")
		if batonPanicHook != nil {
			batonPanicHook()
		}
		b, err := s.finishBaton(in.src, cmd.Text)
		if err != nil {
			fail(err)
			return
		}
		if approve != nil {
			c.notify("Waiting for you to allow this in the Flockdeck window on the computer.", false)
			if err := s.approveElsewhere(context.Background(), *approve); err != nil {
				fail(err)
				return
			}
		}
		saved := batonSavedMsg{Type: "batonSaved", PaneID: in.id, Req: cmd.Req, Cmd: cmd.Cmd, ID: b.ID, Scrubbed: baton.RedactionCount(b.Redactions)}
		task := strings.TrimSpace(cmd.Task)
		switch cmd.Cmd {
		case "saveBaton":
			if err := s.ws.SaveBaton(b); err != nil {
				fail(err)
				return
			}
			if recordSource(b.ID, in.src.Spec.ID) != nil {
				c.notify(sourceNotRecorded, true)
			}
		case "restartWithBaton":
			bp, err := s.ws.PrepareBaton(in.cwd, b, task, in.src.Scrubber())
			if err != nil {
				fail(err)
				return
			}
			if recordSource(bp.ID, in.src.Spec.ID) != nil {
				c.notify(sourceNotRecorded, true)
			}
			type result struct{ err error }
			r, ok := ask(s, func() result { return result{s.ws.RestartWithBaton(in.id, bp, task)} })
			if !ok {
				return
			}
			if r.err != nil {
				fail(r.err)
				return
			}
			saved.Started = in.id
			s.saveLayouts()
			s.Wake()
		case "startFromBaton":
			cwd := in.cwd
			if branch := strings.TrimSpace(cmd.Branch); branch != "" {
				// Held until the agent is running there, like a fan-out's own
				// worktrees; see lockRepo.
				defer lockRepo(cwd)()
				path, err := s.ws.PrepareWorktree(cwd, branch)
				if err != nil {
					fail(err)
					return
				}
				cwd = path
			}
			bp, err := s.ws.PrepareBaton(cwd, b, task, in.src.Scrubber())
			if err != nil {
				fail(err)
				return
			}
			if recordSource(bp.ID, in.src.Spec.ID) != nil {
				c.notify(sourceNotRecorded, true)
			}
			type result struct {
				id  string
				err error
			}
			r, ok := ask(s, func() result {
				id, err := s.ws.Spawn(in.id, workspace.SpawnOptions{
					Task: task, Cwd: cwd, Split: cmd.Split, Kind: session.KindClaude,
					Agent: cmd.Agent, Model: cmd.Model, Baton: &bp,
				})
				if err == nil {
					s.ws.RevealPane(id)
				}
				return result{id, err}
			})
			if !ok {
				return
			}
			if r.err != nil {
				fail(r.err)
				return
			}
			saved.Started = r.id
			s.saveLayouts()
			s.Wake()
		}
		saved.Path = workspace.BatonPath(b.ID)
		c.sendJSON(saved)
	}()
}

// batonPathExts are the files `spawn --baton <path>` reads: notes in text, not
// whatever an agent can name.
var batonPathExts = map[string]bool{".md": true, ".markdown": true, ".txt": true}

// batonReadTimeout bounds how long looking at and reading a notes file may take.
// A regular file on a local disk answers at once; the bound is for one on a
// share that has stopped answering, where even looking at it can wait.
var batonReadTimeout = 5 * time.Second

// networkPath reports whether a path names something other than a local drive
// or a local folder: a UNC share (\\host\share), a device path (\\.\ or \\?\),
// an NT object path (\??\), whichever mix of slashes and backslashes it is
// written in. Windows reads / as \ and the Win32 API accepts those forms with
// either, so the check is made on the path with every slash turned to a
// backslash, whatever system this runs on.
func networkPath(path string) bool {
	p := strings.ReplaceAll(path, "/", `\`)
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `\??\`)
}

// checkBatonPathName is the part of checkBatonPath that only reads the name: it
// is an absolute path to a notes file (.md, .markdown or .txt) on a local drive,
// not a network or device path. The path is cleaned first, and its volume is
// then only a drive letter, or nothing on a system with no volumes.
func checkBatonPathName(path string) error {
	if networkPath(path) || networkPath(filepath.Clean(path)) {
		return fmt.Errorf("%s is a network or device path, which a baton is not read from", path)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("the baton file %q is not an absolute path", path)
	}
	if vol := filepath.VolumeName(filepath.Clean(path)); vol != "" && (len(vol) != 2 || vol[1] != ':') {
		return fmt.Errorf("%s is not on a local drive, which a baton is read from", path)
	}
	if !batonPathExts[strings.ToLower(filepath.Ext(path))] {
		return fmt.Errorf("%s is not a .md, .markdown or .txt file", filepath.Base(path))
	}
	return nil
}

// lstatBatonPath is the part of checkBatonPath that looks at the file: it has to
// be there and be a regular file, not a symbolic link, which could name anything
// the application can read under a harmless name, and not a pipe or a device.
// It touches the file system, so it runs under the deadline.
func lstatBatonPath(path string) error {
	if err := localVolume(path); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("no baton file at %s", path)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; name the file itself", filepath.Base(path))
	}
	if fi.Mode()&os.ModeIrregular != 0 {
		// On Windows a file that is a reparse point other than a link: a OneDrive or
		// other cloud placeholder that is not on this disk, a deduplicated or
		// compressed file, a network redirect. Reading one may fetch it over the
		// network, so it is refused, by name, and the person can copy it and name
		// the copy.
		return fmt.Errorf("%s is a special file (on Windows, a reparse point such as a cloud placeholder that may not be on this disk); copy it to an ordinary file and name that", filepath.Base(path))
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	// A folder above the file may be a link or a junction to somewhere else, and
	// the file is then read from where it points. Where that is a network share
	// it is refused. A drive letter mapped to a share, or made with subst, is
	// refused by localVolume, and the open file is checked again by verifyOpened.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return resolvedLocal(real)
	}
	return nil
}

// resolvedLocal refuses a path that, with its links followed, is a network or
// device path.
func resolvedLocal(real string) error {
	if networkPath(real) {
		return fmt.Errorf("the file is reached through a link to a network or device path (%s), which a baton is not read from", real)
	}
	return nil
}

// checkBatonPath says whether `spawn -baton path:<file>` may read a file. The
// file may be anywhere on a local drive that the application can read; it is
// not read over the network.
func checkBatonPath(path string) error {
	if err := checkBatonPathName(path); err != nil {
		return err
	}
	return lstatBatonPath(path)
}

// readBatonFile reads a notes file for a baton: the name is checked, and then
// looking at the file and reading it are done under a deadline. The file is
// checked again once it is open, in baton.ReadFile, so a path swapped for a link
// between the two is refused.
func readBatonFile(path string, inScope func(string) error) (baton.Baton, error) {
	if err := checkBatonPathName(path); err != nil {
		return baton.Baton{}, err
	}
	type result struct {
		b   baton.Baton
		err error
	}
	done := make(chan result, 1)
	go func() {
		if err := lstatBatonPath(path); err != nil {
			done <- result{err: err}
			return
		}
		// Inside the project of the pane that asked, and only then read.
		if inScope != nil {
			if err := inScope(path); err != nil {
				done <- result{err: err}
				return
			}
		}
		b, err := baton.ReadFileVerified(path, verifyOpened)
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		return r.b, r.err
	case <-time.After(batonReadTimeout):
		return baton.Baton{}, fmt.Errorf("reading %s took longer than %s", filepath.Base(path), batonReadTimeout)
	}
}

// resolveBaton turns the reference an agent gave `flockdeck spawn --baton`
// into a baton: "self", a pane id, a baton id, or "path:" and a file. It is
// resolved here, never taken as text from the request, so a large task string
// cannot be used to carry something past the scrubber, and held to the strict
// profile because nobody reads an agent's baton before it is used: commands are
// listed by program, and a baton or file named by id or path is scrubbed with
// the calling pane's own secrets and rewritten the same way.
//
// A pane id may name a pane of the caller's own project, and no other: Flockdeck
// would be reading another project's conversation as the user. A notes file has
// to be inside the caller's project or checkout, for the same reason. A baton id
// names something the user saved, and is not limited.
//
// parent is the pane that is spawning.
func (s *Server) resolveBaton(parent, ref string) (*handoff, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	type facts struct {
		own, named workspace.BatonSource
		haveOwn    bool
		haveNamed  bool
		err        error
	}
	var in facts
	if src, err := s.ws.BatonSource(parent); err == nil {
		in.own, in.haveOwn = src, true
	}
	switch {
	case ref == "self":
		if !in.haveOwn {
			in.err = errors.New("--baton self needs an agent pane to make the baton from")
		}
	case baton.ValidID(ref), strings.HasPrefix(ref, "path:"):
	default:
		src, err := s.ws.BatonSource(ref)
		if err != nil {
			in.err = fmt.Errorf("%q is not self, a pane, a baton id or a file: %v", ref, err)
		}
		if err == nil && !s.ws.BatonPaneInScope(parent, ref) {
			in.err = fmt.Errorf("pane %q is in another project. An agent may only have a baton made from a pane of its own project; ask the user to make and save one, and name its id", ref)
			src, err = workspace.BatonSource{}, errors.New("out of scope")
		}
		in.named, in.haveNamed = src, err == nil
	}
	if in.err != nil {
		return nil, in.err
	}
	h := &handoff{Scrubber: baton.NewScrubber()}
	if in.haveOwn {
		h.Scrubber = in.own.Scrubber()
	}
	switch {
	case ref == "self":
		h.Baton, h.Source = in.own.Make(true), in.own.Spec.ID
	case in.haveNamed:
		h.Baton, h.Source = in.named.Make(true), in.named.Spec.ID
	case baton.ValidID(ref):
		st, err := baton.Open()
		if err != nil {
			return nil, err
		}
		b, err := st.Load(ref)
		if err != nil {
			return nil, fmt.Errorf("no baton %s: %w", ref, err)
		}
		// Used, so it does not age out: the original, and not only the fork that
		// hardening may make of it.
		st.Touch(ref)
		h.Source = st.Source(ref)
		h.Baton = hardened(b, h.Scrubber)
	default:
		b, err := readBatonFile(strings.TrimPrefix(ref, "path:"), func(p string) error { return s.ws.BatonPathInScope(parent, p) })
		if err != nil {
			return nil, err
		}
		// A file is notes. What its header claims about where it came from is a
		// claim, and is dropped: it is shown to the new agent as what it is.
		b.ID, b.FromPane, b.FromAgent, b.FromModel = "", "", "", ""
		h.Baton = hardened(b, h.Scrubber)
	}
	return h, nil
}

// hardened applies the strict profile and a scrub to a baton an agent named,
// and gives the result an id of its own when that changed anything, so what
// was stored under the old id stays what it was.
func hardened(b baton.Baton, sc *baton.Scrubber) baton.Baton {
	before := baton.Render(b)
	out := sc.ScrubBaton(baton.Harden(b))
	if out.ID != "" && baton.Render(out) == before {
		return out
	}
	if out.ID == "" {
		out.ID = baton.NewID(time.Now())
		out.Created = time.Now()
		return out
	}
	return out.Fork(time.Now())
}
