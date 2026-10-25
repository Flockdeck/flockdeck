package workspace

import (
	"fmt"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/team"
)

// A pane's label is what it is called for the work it does: "rate limiter PR",
// not the name of its directory, which every helper of one project shares. It is
// set when the pane is spawned (spawn -name, or a phrase made from its task) and
// changed later by the pane itself or by a manager (`flockdeck pane-name`) or by
// the person at the window. It is kept with the pane, so a restart brings it
// back.

// LabelAutoOf reports whether a pane's label was made from its task.
func (w *Workspace) LabelAutoOf(p *Pane) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return p.LabelAuto
}

// LabelOf reports a pane's label. It is written from the hook server's
// goroutines, so it is read under the lock.
func (w *Workspace) LabelOf(p *Pane) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return p.Label
}

// labelFromTask makes a short label from the text a pane was started with: the
// first clause of its first line, without markdown, cut at a word. It is empty
// when the text is a slash command or a synthetic message, or has no words.
func labelFromTask(task string, roles ...string) string {
	raw := strings.TrimSpace(task)
	if raw == "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "<") {
		return ""
	}
	line := ""
	for _, l := range strings.Split(raw, "\n") {
		if l = strings.TrimSpace(plainPrompt(l)); l != "" && !isFence(strings.TrimSpace(l)) {
			line = l
			break
		}
	}
	line = strings.Join(strings.Fields(line), " ")
	for _, sep := range []string{". ", ": ", "; ", " - ", " — ", " – ", ", "} {
		if i := strings.Index(line, sep); i > 0 {
			line = line[:i]
		}
	}
	line = strings.TrimRight(line, ".:;,- ")
	r := []rune(line)
	if len(r) > team.MaxPaneLabel {
		cut := team.MaxPaneLabel
		for i := team.MaxPaneLabel - 1; i > team.MaxPaneLabel*2/3; i-- {
			if r[i] == ' ' {
				cut = i
				break
			}
		}
		line = strings.TrimSpace(string(r[:cut]))
	}
	return team.PaneLabelText(line, roles...)
}

// labelRoles are the role ids a label may not pass for: those of the catalog,
// custom roles among them. SetPaneLabel adds the roles the panes of the project run as.
func (w *Workspace) labelRoles() []string {
	if c := w.agents(); c != nil {
		return c.RoleIDs()
	}
	return nil
}

// A pane's label may be changed this often by others in a window of time.
const (
	maxRenames      = 6
	maxActorRenames = 12
	renameWindow    = time.Minute
)

// SetPaneLabel names pane id for its work. actor is the pane asking, or empty for
// the person at the window, who may name any pane. A pane may name itself; only a
// manager may name another pane, and only one of its own project. This is a
// courtesy check on the pane id the caller reports, not a security boundary: every
// pane holds the same token, as for messaging, so a pane can say it is another.
// Text is cleaned
// and cut (team.PaneLabelText); empty clears the label. It reports the label
// stored. It must run on the workspace goroutine.
func (w *Workspace) SetPaneLabel(actor, id, text string) (string, error) {
	p := w.Pane(id)
	if p == nil {
		return "", fmt.Errorf("no pane %q is open here", id)
	}
	if actor != "" && actor != id {
		members, err := w.Roster(actor)
		if err != nil {
			return "", err
		}
		manager, inProject := false, false
		for _, m := range members {
			if m.Pane == actor && m.Role == team.ManagerRole {
				manager = true
			}
			if m.Pane == id {
				inProject = true
			}
		}
		if !manager {
			return "", fmt.Errorf("only a manager can name another pane; name your own with flockdeck pane-name <text>")
		}
		if !inProject {
			return "", fmt.Errorf("pane %q is not in your project", id)
		}
	}
	label := team.PaneLabelText(text, append(w.labelRoles(), w.projectRoles(id)...)...)
	w.mu.Lock()
	old := p.Label
	changed := p.Label != label || p.LabelAuto
	burst := 0
	if changed && actor != "" {
		// An agent may rename a pane only so often, and rename only so many panes, so a loop cannot flood the window with changes.
		now := time.Now()
		recent := recentTimes(w.labelRenames[id], now)
		mine := recentTimes(w.actorRenames[actor], now)
		if len(recent) >= maxRenames {
			w.labelRenames[id] = recent
			w.mu.Unlock()
			return "", fmt.Errorf("pane %s was renamed %d times in the last minute; wait before naming it again", id, maxRenames)
		}
		if len(mine) >= maxActorRenames {
			w.actorRenames[actor] = mine
			w.mu.Unlock()
			return "", fmt.Errorf("you have renamed panes %d times in the last minute; wait before naming another", maxActorRenames)
		}
		if w.labelRenames == nil {
			w.labelRenames = map[string][]time.Time{}
		}
		if w.actorRenames == nil {
			w.actorRenames = map[string][]time.Time{}
		}
		w.labelRenames[id] = append(recent, now)
		w.actorRenames[actor] = append(mine, now)
		burst = len(recent) + 1
	}
	p.Label, p.LabelAuto = label, false
	w.mu.Unlock()
	if changed {
		if actor != "" && actor != id {
			w.recordRename(actor, id, old, label, burst)
		}
		w.wake()
	}
	return label, nil
}

// PaneNamed applies `flockdeck pane-name`: the caller's own pane when no target
// is named, else the target. It is what hooks.SetPaneNameHandler is given. It
// must run on the workspace goroutine.
func (w *Workspace) PaneNamed(req hooks.PaneNameRequest) (hooks.PaneNameResult, error) {
	if w.Pane(req.Pane) == nil {
		return hooks.PaneNameResult{}, fmt.Errorf("no pane %q is open here", req.Pane)
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = req.Pane
	}
	label, err := w.SetPaneLabel(req.Pane, target, req.Text)
	if err != nil {
		return hooks.PaneNameResult{}, err
	}
	return hooks.PaneNameResult{Pane: target, Label: label}, nil
}

// projectRoles are the role ids of the panes in pane id's project, which a label
// may not pass for.
func (w *Workspace) projectRoles(id string) []string {
	members, err := w.Roster(id)
	if err != nil {
		return nil
	}
	var roles []string
	for _, m := range members {
		if m.Role != "" {
			roles = append(roles, m.Role)
		}
	}
	return roles
}

// recentTimes is the times in at that fall in the rename window ending at now.
func recentTimes(at []time.Time, now time.Time) []time.Time {
	recent := at[:0:0]
	for _, t := range at {
		if now.Sub(t) < renameWindow {
			recent = append(recent, t)
		}
	}
	return recent
}

// forgetRenames drops what the rename limits remember of a pane that is closed.
// It runs with w.mu held.
func (w *Workspace) forgetRenames(id string) {
	delete(w.labelRenames, id)
	delete(w.actorRenames, id)
	if t := w.renameTrails[id]; t != nil && t.timer != nil {
		t.timer.Stop()
	}
	delete(w.renameTrails, id)
}

// RenameInfo is a rename of a pane by another, for the person at the window. Was
// is the name the pane had when a burst of renames began, Now the latest, and N how
// many renames the burst has had so far. Names are cut and have no format character.
type RenameInfo struct {
	Actor, ActorName, Target, Was, Now string
	N                                  int
}

// SetRenameNotifier sets what is told of renames by other panes: the first of a
// burst at once, then one more after the burst has been quiet for a moment, with
// the latest name and the count. It is called from the workspace goroutine and,
// for the trailing one, from a timer.
func (w *Workspace) SetRenameNotifier(f func(RenameInfo)) {
	w.mu.Lock()
	w.renameNotify = f
	w.mu.Unlock()
}

// DisplayName is how a pane is called: its label, else its name, as text for a
// notice. It must run on the workspace goroutine.
func (w *Workspace) DisplayName(id string) string {
	if p := w.Pane(id); p != nil {
		return team.PlainName(team.Member{Name: p.Name, Peer: w.PeerNameOf(p), Called: w.LabelOf(p)}.Label(), 40)
	}
	return id
}

// renameTrail is a burst of renames of one pane waiting for its trailing notice.
type renameTrail struct {
	timer *time.Timer
	info  RenameInfo
	who   string
}

// renameQuiet is how long a burst of renames must be quiet before its last state is told.
var renameQuiet = 3 * time.Second

// recordRename logs a rename by another pane in the team event log (who, which
// pane, the old label and the new) every time; the log keeps the newest 200. The
// pane's inbox and the person are told of the first rename of a burst at once, and
// of the last state of the burst once it is quiet, so a few renames in a row are
// two notices and the pane is never left believing an older name. burst is how many
// renames of this pane there have been in the last minute, this one included.
func (w *Workspace) recordRename(actor, id, old, label string, burst int) {
	w.recordEvent(team.Event{Kind: team.EventRename, Time: time.Now(), From: actor, To: id, Old: old, New: label})
	who := "pane " + actor
	if members, err := w.Roster(id); err == nil {
		for _, m := range members {
			if m.Pane == actor {
				who = fmt.Sprintf("%s %q (pane %s)", m.Role, team.PlainName(m.Label(), team.MaxPaneLabel), actor)
				break
			}
		}
	}
	info := RenameInfo{Actor: actor, ActorName: w.DisplayName(actor), Target: id, Was: team.PlainName(old, team.MaxPaneLabel), Now: label, N: 1}
	w.mu.Lock()
	notify := w.renameNotify
	if burst > 1 {
		if w.renameTrails == nil {
			w.renameTrails = map[string]*renameTrail{}
		}
		t := w.renameTrails[id]
		if t == nil {
			t = &renameTrail{info: info}
			w.renameTrails[id] = t
		}
		t.info.Actor, t.info.ActorName, t.info.Target, t.info.Now, t.who = actor, info.ActorName, id, label, who
		t.info.N = burst
		if t.timer != nil {
			t.timer.Stop()
		}
		t.timer = time.AfterFunc(renameQuiet, func() { w.tellRenameBurst(id) })
		w.mu.Unlock()
		return
	}
	// The name this burst began from, for the summary if more renames follow.
	if w.renameTrails == nil {
		w.renameTrails = map[string]*renameTrail{}
	}
	w.renameTrails[id] = &renameTrail{info: info}
	w.mu.Unlock()
	if w.team.inbox != nil {
		text := fmt.Sprintf("Pane name changed. Set by %s, as the command reported its pane id. Was %q, now %q.", who, info.Was, label)
		if label == "" {
			text = fmt.Sprintf("Pane name cleared. Set by %s, as the command reported its pane id. Was %q.", who, info.Was)
		}
		w.team.inbox.SendSystem(id, text)
	}
	if notify != nil {
		notify(info)
	}
}

// tellRenameBurst tells the last state of a burst of renames, once it is quiet.
func (w *Workspace) tellRenameBurst(id string) {
	w.mu.Lock()
	t := w.renameTrails[id]
	delete(w.renameTrails, id)
	notify := w.renameNotify
	w.mu.Unlock()
	if t == nil || t.timer == nil {
		return
	}
	if w.team.inbox != nil {
		w.team.inbox.SendSystem(id, fmt.Sprintf("Pane name changed %d times in the last minute. Set by %s, as the command reported its pane id. Was %q at the start, now %q.", t.info.N, t.who, t.info.Was, t.info.Now))
	}
	if notify != nil {
		notify(t.info)
	}
}
