package tasks

import (
	"fmt"
	"strings"

	"github.com/jmwri/flockdeck/internal/team"
)

// ResolveAssignee finds who a task is to be assigned to. The target is, in this
// order: the id of a pane, a peer name or pane name, or a role id; "role:" in
// front of an id makes it a role only. roles are the ids of the roles there are,
// so a role with no pane running can still be assigned a task. Unlike a
// message's target the actor's own pane is allowed, since a manager may keep a
// task for itself.
func ResolveAssignee(members []team.Member, roles []string, target string) (Assignee, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return Assignee{}, fmt.Errorf("say who the task is for: a role, a name or a pane id")
	}
	role, onlyRole := strings.CutPrefix(target, "role:")
	if onlyRole {
		return roleAssignee(members, roles, strings.ToLower(strings.TrimSpace(role)))
	}
	for _, m := range members {
		if m.Pane == target {
			return paneAssignee(m)
		}
	}
	var named []team.Member
	for _, m := range members {
		if strings.EqualFold(m.Peer, target) || strings.EqualFold(m.Name, target) {
			named = append(named, m)
		}
	}
	switch len(named) {
	case 1:
		return paneAssignee(named[0])
	case 0:
	default:
		var lines []string
		for _, m := range named {
			lines = append(lines, "  "+m.Line())
		}
		return Assignee{}, fmt.Errorf("%q is the name of %d panes; assign to one by its pane id:\n%s", team.PlainLabel(target), len(named), strings.Join(lines, "\n"))
	}
	if got, err := roleAssignee(members, roles, strings.ToLower(target)); err == nil {
		return got, nil
	}
	return Assignee{}, fmt.Errorf("no pane, name or role matches %q in this project; `flockdeck team` lists who is here", team.PlainLabel(target))
}

func paneAssignee(m team.Member) (Assignee, error) {
	if m.Exited() {
		return Assignee{}, fmt.Errorf("the pane %s (%s) has exited, so it would never see the task", m.Pane, team.PlainLabel(m.Label()))
	}
	return Assignee{Pane: m.Pane, Role: m.Role, Name: m.Label()}, nil
}

func roleAssignee(members []team.Member, roles []string, role string) (Assignee, error) {
	if role == "" {
		return Assignee{}, fmt.Errorf("role: needs a role id")
	}
	for _, r := range roles {
		if r == role {
			return Assignee{Role: role}, nil
		}
	}
	for _, m := range members {
		if m.Role == role {
			return Assignee{Role: role}, nil
		}
	}
	return Assignee{}, fmt.Errorf("no role called %q", team.PlainLabel(role))
}

// Notice is one message Flockdeck writes to a pane because of a change.
type Notice struct {
	Pane string
	Text string
}

// Notices says who is told of a change, and what. The members are the roster
// of the actor's project. A pane is told at most once for a change; the actor is
// never told of their own; and a pane that is not on the roster, or whose
// process has exited, is not told at all, since nothing would read it.
//
// The manager is told when a task goes to review with no reviewer to take it,
// is blocked, is done by somebody else, or gets a note while it is in review.
// The manager is every live pane in the manager role, or the live panes with no
// role when there is none, as in the rules.
//
// The text is built from the task's title, which is one line, and the cleaned
// labels of the people in it. A note's own words appear only in a message that
// is read with `flockdeck inbox read`, cut to one line, and never in the line
// typed into a terminal, which carries no message text.
func Notices(ch Change, actor Actor, members []team.Member) []Notice {
	t := ch.Task
	live := map[string]team.Member{}
	for _, m := range members {
		if !m.Exited() {
			live[m.Pane] = m
		}
	}
	var out []Notice
	told := map[string]bool{actor.Pane: true}
	tell := func(pane, text string) {
		if pane == "" || told[pane] {
			return
		}
		if _, ok := live[pane]; !ok {
			return
		}
		told[pane] = true
		out = append(out, Notice{Pane: pane, Text: text})
	}
	inRole := func(role string) []string {
		var ids []string
		for _, m := range members {
			if m.Role == role && !m.Exited() {
				ids = append(ids, m.Pane)
			}
		}
		return ids
	}
	owners := func() []string {
		if t.OwnerPane != "" {
			return []string{t.OwnerPane}
		}
		if t.OwnerRole != "" {
			return inRole(t.OwnerRole)
		}
		return nil
	}
	var managers []string
	for _, m := range members {
		if m.Role == "manager" && !m.Exited() {
			managers = append(managers, m.Pane)
		}
	}
	if len(managers) == 0 {
		managers = inRole("")
	}
	actorManages := false
	for _, p := range managers {
		if p == actor.Pane {
			actorManages = true
		}
	}
	tellManagers := func(text string) {
		for _, p := range managers {
			tell(p, text)
		}
	}
	who := actor.Label()

	switch ch.Kind {
	case Assign:
		if t.OwnerPane != "" {
			tell(t.OwnerPane, fmt.Sprintf("task %s assigned to you: %s", t.ID, t.Title))
		} else {
			for _, p := range inRole(t.OwnerRole) {
				tell(p, fmt.Sprintf("task %s assigned to your role (%s): %s", t.ID, t.OwnerRole, t.Title))
			}
		}
	case Move:
		switch ch.Task.State {
		case InReview:
			text := fmt.Sprintf("task %s is ready for review: %s", t.ID, t.Title)
			if t.Branch != "" {
				text += " (branch " + t.Branch + ")"
			}
			var reviewers []string
			for _, role := range []string{"reviewer", "tester"} {
				for _, p := range inRole(role) {
					if p != actor.Pane {
						reviewers = append(reviewers, p)
					}
				}
			}
			for _, p := range reviewers {
				tell(p, text)
			}
			if len(reviewers) == 0 {
				tell(t.Creator.Pane, text)
				tellManagers(text)
			}
		case Blocked:
			text := fmt.Sprintf("task %s is blocked, by %s: %s", t.ID, who, t.Title) + noteTail(ch.Note)
			tell(t.Creator.Pane, text)
			tellManagers(text)
		case Done:
			text := fmt.Sprintf("task %s is done: %s", t.ID, t.Title)
			tell(t.Creator.Pane, text)
			if !actorManages {
				tellManagers(fmt.Sprintf("task %s is done, by %s: %s", t.ID, who, t.Title))
			}
		case Cancelled:
			for _, p := range owners() {
				tell(p, fmt.Sprintf("task %s was cancelled by %s: %s", t.ID, who, t.Title)+noteTail(ch.Note))
			}
		}
		// A note that came with the move reaches the owner and the creator, as a
		// note of its own would, unless the move already told them.
		if ch.Note != "" {
			text := fmt.Sprintf("task %s is now %s, by %s: %s", t.ID, t.State, who, t.Title) + noteTail(ch.Note)
			for _, p := range owners() {
				tell(p, text)
			}
			tell(t.Creator.Pane, text)
		}
	case NoteKind:
		text := fmt.Sprintf("note on task %s from %s: %s", t.ID, who, team.PlainText(ch.Note, 200))
		for _, p := range owners() {
			tell(p, text)
		}
		tell(t.Creator.Pane, text)
		if t.State == InReview {
			tellManagers(text)
		}
	}
	return out
}

func noteTail(note string) string {
	if note == "" {
		return ""
	}
	return " (note: " + team.PlainText(note, 200) + ")"
}
