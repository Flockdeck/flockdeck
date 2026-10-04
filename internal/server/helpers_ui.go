package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/helpers"
)

// The Helpers dialog's side of helper apps: control messages from a window.
// They are the same actions the CLI has (see helpers.go) and are desk-only: a
// window reached through the relay is not offered them, since they install a
// program on, and open a browser on, the machine Flockdeck runs on.

// HelperRow is one helper as the dialog draws it.
type HelperRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Installed is the installed version, "" when there is none.
	Installed string `json:"installed,omitempty"`
	// Signed is false for a version installed through the unsigned override.
	Signed bool `json:"signed"`
	// State is "notinstalled", "installing", or a helpers.State.
	State    string   `json:"state"`
	URL      string   `json:"url,omitempty"`
	Err      string   `json:"error,omitempty"`
	Log      []string `json:"log,omitempty"`
	Restarts int      `json:"restarts,omitempty"`
	// Owner is "verified" or "unverified": whether the system confirmed that the
	// helper's own process holds its port.
	Owner string `json:"owner,omitempty"`
	// Update is a newer version found when the dialog was opened.
	Update  string   `json:"update,omitempty"`
	Allows  []string `json:"allows"`
	DataDir string   `json:"dataDir,omitempty"`
	HasData bool     `json:"hasData,omitempty"`
}

type helpersMsg struct {
	Type  string      `json:"type"`
	Rows  []HelperRow `json:"rows"`
	Error string      `json:"error,omitempty"`
}

// helperPlanMsg is what an install would do, shown before anything is
// downloaded. Error, with Fatal set, is a refusal that has no override.
type helperPlanMsg struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Signed    bool   `json:"signed"`
	Installed string `json:"installed,omitempty"`
	// Repair says the installed version no longer matches what was installed,
	// and this puts a checked copy of it back.
	Repair bool     `json:"repair,omitempty"`
	Allows []string `json:"allows,omitempty"`
	Error  string   `json:"error,omitempty"`
	Fatal  bool     `json:"fatal,omitempty"`
}

// helperState is what the dialog needs beyond the supervisor and the disk.
type helperState struct {
	mu     sync.Mutex
	latest map[string]string // newest version found, by helper id
	busy   map[string]bool   // installing
}

func (h *helperState) setBusy(id string, v bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy == nil {
		h.busy = map[string]bool{}
	}
	if v && h.busy[id] {
		return false
	}
	h.busy[id] = v
	return true
}

func (h *helperState) isBusy(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.busy[id]
}

func (h *helperState) setLatest(id, v string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.latest == nil {
		h.latest = map[string]string{}
	}
	h.latest[id] = v
}

func (h *helperState) latestOf(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.latest[id]
}

// helperRows builds every helper's row.
func (s *Server) helperRows() []HelperRow {
	sup, inst := s.helperSupervisor()
	if sup == nil || inst == nil {
		return nil
	}
	st := inst.Store()
	var rows []HelperRow
	for _, e := range helpers.Catalogue() {
		row := HelperRow{ID: e.ID, Name: e.Name, Summary: e.Summary, Allows: e.Allows, State: "notinstalled", Signed: true}
		if info, ok := st.Info(e.ID); ok {
			row.Installed, row.Signed = info.Version, info.Signed
			status := sup.Status(e.ID)
			row.State = string(status.State)
			row.URL, row.Err, row.Log, row.Restarts, row.Owner = status.URL, status.Err, status.Log, status.Restarts, status.Owner
			if latest := s.helperUI.latestOf(e.ID); latest != "" && helpers.UpdateAvailable(e, latest, info.Version) {
				row.Update = latest
			}
		}
		if s.helperUI.isBusy(e.ID) {
			row.State = "installing"
		}
		if st.HasData(e.ID) {
			row.HasData, row.DataDir = true, st.DataDir(e.ID)
		}
		rows = append(rows, row)
	}
	return rows
}

// HelperChanged tells every desk window that a helper's status changed. The
// supervisor's Notify calls it.
func (s *Server) HelperChanged() {
	if s.ClientCount() == 0 {
		return
	}
	for _, c := range s.clientList() {
		if !c.remote {
			c.sendJSON(helpersMsg{Type: "helpers", Rows: s.helperRows()})
		}
	}
}

// helperDeskOnly refuses a window reached through the relay, and a server that
// has no supervisor.
func (s *Server) helperDeskOnly(c *controlClient) bool {
	if c.remote {
		c.notify("Helper apps are managed on the machine Flockdeck runs on, not from a window reached through the relay", true)
		return false
	}
	if sup, _ := s.helperSupervisor(); sup == nil {
		c.notify("Helper apps are not available in this instance", true)
		return false
	}
	return true
}

// helperDeskOnlyFor is helperDeskOnly for a command that names a helper, which
// has to be one in the catalogue: an id that merely looks like one is never
// turned into a path or a process.
func (s *Server) helperDeskOnlyFor(c *controlClient, id string) bool {
	if !s.helperDeskOnly(c) {
		return false
	}
	if _, ok := helpers.Lookup(id); !ok {
		c.notify("That is not a helper Flockdeck knows", true)
		return false
	}
	return true
}

// helperInstalling reports whether an install of a helper is under way.
func (s *Server) helperInstalling(id string) bool {
	_, inst := s.helperSupervisor()
	return s.helperUI.isBusy(id) || (inst != nil && inst.Installing(id))
}

const helperNetworkWait = 30 * time.Minute

// helpersList answers the dialog being opened, then looks for newer versions
// of what is installed, which is the only time Flockdeck asks the source anything
// about helpers unless a person installs one.
func (s *Server) helpersList(c *controlClient) {
	if !s.helperDeskOnly(c) {
		return
	}
	c.sendJSON(helpersMsg{Type: "helpers", Rows: s.helperRows()})
	_, inst := s.helperSupervisor()
	go func() {
		defer s.surviveFor(c, "checking helpers for updates")
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		changed := false
		for _, e := range helpers.Catalogue() {
			if _, ok := inst.Store().Current(e.ID); !ok {
				continue
			}
			newer, err := inst.CheckUpdate(ctx, e.ID)
			if err != nil {
				continue
			}
			if s.helperUI.latestOf(e.ID) != newer {
				s.helperUI.setLatest(e.ID, newer)
				changed = true
			}
		}
		if changed {
			c.sendJSON(helpersMsg{Type: "helpers", Rows: s.helperRows()})
		}
	}()
}

// helperPlan fetches and checks a release and sends what installing it would
// do. Nothing is downloaded but two small files, and nothing is written.
func (s *Server) helperPlan(c *controlClient, id, version string) {
	if !s.helperDeskOnlyFor(c, id) {
		return
	}
	_, inst := s.helperSupervisor()
	go func() {
		defer s.surviveFor(c, "looking up a helper")
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		plan, err := inst.PlanVersion(ctx, id, version, false)
		if err != nil {
			var sig *helpers.SignatureError
			var required *helpers.SignedRequiredError
			c.sendJSON(helperPlanMsg{Type: "helperPlan", ID: id, Error: planError(err), Fatal: errors.As(err, &sig) || errors.As(err, &required)})
			return
		}
		c.sendJSON(helperPlanMsg{
			Type: "helperPlan", ID: id, Name: plan.Entry.Name, Summary: plan.Entry.Summary, Version: plan.Version, URL: plan.URL,
			SHA256: plan.SHA256, Signed: plan.Signed, Installed: plan.Installed, Repair: plan.Repair, Allows: plan.Entry.Allows,
		})
	}()
}

// planError is an install failure in words for the dialog.
func planError(err error) string {
	switch {
	case errors.Is(err, helpers.ErrNoAsset):
		return "There is no build of this helper for this platform."
	case errors.Is(err, helpers.ErrAlreadyInstalled):
		return err.Error()
	case errors.Is(err, helpers.ErrBelowHighWater):
		return "This release is older than a signed version that was installed here before, so it is not offered. It can be installed by naming the version on the command line."
	}
	var sig *helpers.SignatureError
	if errors.As(err, &sig) {
		return err.Error() + ". Nothing was installed and this cannot be overridden."
	}
	return err.Error()
}

// helperInstall carries out an install the person confirmed in the dialog. The
// release is looked up again, and has to be the one that was shown: the same
// version with the same archive hash. An unsigned one goes ahead only when the
// window says the override was chosen.
func (s *Server) helperInstall(c *controlClient, cmd command) {
	if !s.helperDeskOnlyFor(c, cmd.ID) {
		return
	}
	if !cmd.Confirmed {
		return
	}
	sup, inst := s.helperSupervisor()
	id, version := cmd.ID, cmd.Text
	if !s.helperUI.setBusy(id, true) {
		c.notify("That helper is already being installed", true)
		return
	}
	s.HelperChanged()
	go func() {
		defer s.surviveFor(c, "installing a helper")
		defer func() { s.helperUI.setBusy(id, false); s.HelperChanged() }()
		ctx, cancel := context.WithTimeout(context.Background(), helperNetworkWait)
		defer cancel()
		if sup.Active(id) {
			c.notify("Stop the helper before installing over it", true)
			return
		}
		plan, err := inst.PlanVersion(ctx, id, version, false)
		if err != nil {
			c.notify("Not installed: "+planError(err), true)
			return
		}
		if plan.SHA256 != cmd.SHA256 || plan.Version != version {
			c.notify("Not installed: the release changed since it was shown. Look again before installing.", true)
			return
		}
		if !plan.Signed && !cmd.Unsigned {
			c.notify("Not installed: the release has no signature, and the unsigned override was not chosen.", true)
			return
		}
		info, err := inst.InstallPlan(ctx, plan)
		if err != nil {
			c.notify("Not installed: "+err.Error(), true)
			return
		}
		s.helperUI.setLatest(id, "")
		note := fmt.Sprintf("Installed %s %s", plan.Entry.Name, info.Version)
		if !info.Signed {
			note += " (unsigned)"
		}
		c.notify(note, false)
	}()
}

func (s *Server) helperStart(c *controlClient, id string) {
	if !s.helperDeskOnlyFor(c, id) {
		return
	}
	sup, _ := s.helperSupervisor()
	if s.helperInstalling(id) {
		c.notify("That helper is being installed; wait for that to finish", true)
		return
	}
	if _, err := sup.Start(id); err != nil {
		c.notify(err.Error(), true)
	}
	s.HelperChanged()
}

func (s *Server) helperStop(c *controlClient, id string) {
	if !s.helperDeskOnlyFor(c, id) {
		return
	}
	sup, _ := s.helperSupervisor()
	go func() {
		defer s.surviveFor(c, "stopping a helper")
		_ = sup.Stop(id)
		s.HelperChanged()
	}()
}

func (s *Server) helperOpen(c *controlClient, id string) {
	if !s.helperDeskOnlyFor(c, id) {
		return
	}
	sup, _ := s.helperSupervisor()
	st := sup.Status(id)
	if st.State != helpers.StateRunning || st.URL == "" {
		c.notify("Start the helper first", true)
		return
	}
	if err := openURL(st.URL); err != nil {
		c.notify("Could not open the browser: "+err.Error(), true)
	}
}

func (s *Server) helperUninstall(c *controlClient, id string, purge bool) {
	if !s.helperDeskOnlyFor(c, id) {
		return
	}
	sup, inst := s.helperSupervisor()
	if s.helperInstalling(id) {
		c.notify("That helper is being installed; wait for that to finish", true)
		return
	}
	if sup.Active(id) {
		c.notify("Stop the helper before removing it", true)
		return
	}
	if err := inst.Store().Uninstall(id, purge); err != nil {
		c.notify("Not removed: "+err.Error(), true)
	} else if purge {
		c.notify("Removed, and its data folder deleted", false)
	} else {
		c.notify("Removed. Its data folder was kept.", false)
	}
	s.HelperChanged()
}
