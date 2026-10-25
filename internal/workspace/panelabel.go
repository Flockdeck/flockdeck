package workspace

import (
	"fmt"
	"strings"

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
func labelFromTask(task string) string {
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
	return team.PaneLabelText(line)
}

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
	label := team.PaneLabelText(text, w.projectRoles(id)...)
	w.mu.Lock()
	changed := p.Label != label || p.LabelAuto
	p.Label, p.LabelAuto = label, false
	w.mu.Unlock()
	if changed {
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
