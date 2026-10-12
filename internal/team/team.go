// Package team is what lets the panes of one project know about each other and
// talk: the roster of who is running, the messages one pane sends another, and
// the rule for when Flockdeck may tell a pane it has mail.
//
// It holds no panes, sessions or files. The workspace builds the roster from its
// own state, stores what Store.Snapshot returns, and types the notice a Notifier
// produces, so everything here can be tested without a terminal.
package team

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// System is the sender name of a message Flockdeck wrote itself.
const System = "flockdeck"

// Member is one agent pane of a project, as the roster lists it.
type Member struct {
	Pane string `json:"pane"`
	// Name is the pane's own name, usually the basename of its directory, and
	// Peer the name the agent reported with `flockdeck peer-name`, if it did.
	Name string `json:"name"`
	Peer string `json:"peer,omitempty"`
	// Role is the id of the role the pane was started as, empty for none.
	Role   string `json:"role,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"`
	Status string `json:"status"`
	// Self marks the pane the roster was asked for.
	Self bool `json:"self,omitempty"`
}

// Label is what to call the member: its peer name, or its pane name.
func (m Member) Label() string {
	if m.Peer != "" {
		return m.Peer
	}
	return m.Name
}

// Exited reports that the member's process is gone.
func (m Member) Exited() bool { return m.Status == "exited" }

// Line is the member as one line of the roster.
func (m Member) Line() string {
	role := m.Role
	if role == "" {
		role = "no role"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %q", role, m.Label())
	if m.Peer != "" && m.Name != "" && m.Peer != m.Name {
		fmt.Fprintf(&b, " (pane name %q)", m.Name)
	}
	if m.Agent != "" {
		b.WriteString(", " + m.Agent)
		if m.Model != "" {
			b.WriteString(" · " + m.Model)
		}
	}
	fmt.Fprintf(&b, ", %s, pane %s", m.Status, m.Pane)
	if m.Self {
		b.WriteString(" (you)")
	}
	return b.String()
}

// Lines writes the roster as lines, at most limit of them, and says how many
// members it left out. The member marked Self is kept when the list is cut, and
// so are the ones that are not exited, ahead of the ones that are.
func Lines(members []Member, limit int) (lines []string, omitted int) {
	ordered := slices.Clone(members)
	slices.SortStableFunc(ordered, func(a, b Member) int {
		return rank(a) - rank(b)
	})
	if limit > 0 && len(ordered) > limit {
		omitted = len(ordered) - limit
		ordered = ordered[:limit]
	}
	// Back in the order the caller gave, so the cut changes what is listed and
	// not where it sits.
	keep := make(map[string]bool, len(ordered))
	for _, m := range ordered {
		keep[m.Pane] = true
	}
	for _, m := range members {
		if keep[m.Pane] {
			lines = append(lines, m.Line())
		}
	}
	return lines, omitted
}

func rank(m Member) int {
	switch {
	case m.Self:
		return 0
	case m.Exited():
		return 2
	}
	return 1
}

// Resolve finds the members a message to target goes to. The target is, in
// this order: the id of a pane, a peer name or pane name, or a role id; "role:"
// in front of an id makes it a role only. Names are matched without regard to
// case. A role fans out to every live pane in it except the sender's own.
//
// A name that several panes answer to is an error that lists them, since
// picking one would send the message to a pane its sender may not have meant.
func Resolve(members []Member, target string) ([]Member, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("say who the message is for: a pane id, a name or a role")
	}
	role, onlyRole := strings.CutPrefix(target, "role:")
	if onlyRole {
		return resolveRole(members, strings.ToLower(strings.TrimSpace(role)))
	}

	for _, m := range members {
		if m.Pane == target {
			return one(m)
		}
	}
	var named []Member
	var self *Member
	for i, m := range members {
		if strings.EqualFold(m.Peer, target) || strings.EqualFold(m.Name, target) {
			if m.Self {
				self = &members[i]
				continue
			}
			named = append(named, m)
		}
	}
	switch len(named) {
	case 1:
		return one(named[0])
	case 0:
		if self != nil {
			return one(*self)
		}
	default:
		var lines []string
		for _, m := range named {
			lines = append(lines, "  "+m.Line())
		}
		return nil, fmt.Errorf("%q is the name of %d panes; send to one by its pane id:\n%s", target, len(named), strings.Join(lines, "\n"))
	}
	if got, err := resolveRole(members, strings.ToLower(target)); got != nil || !isNoSuchRole(err) {
		return got, err
	}
	return nil, fmt.Errorf("no pane, name or role matches %q in this project; `flockdeck team` lists who is here", target)
}

type noSuchRole struct{ role string }

func (e noSuchRole) Error() string {
	return fmt.Sprintf("no other pane in this project has the role %q", e.role)
}

func isNoSuchRole(err error) bool {
	_, ok := err.(noSuchRole)
	return ok
}

func one(m Member) ([]Member, error) {
	if m.Self {
		return nil, fmt.Errorf("that is this pane; a message to yourself goes nowhere")
	}
	if m.Exited() {
		return nil, fmt.Errorf("the pane %s (%s) has exited, so it would never read the message", m.Pane, m.Label())
	}
	return []Member{m}, nil
}

func resolveRole(members []Member, role string) ([]Member, error) {
	if role == "" {
		return nil, fmt.Errorf("role: needs a role id")
	}
	var out []Member
	any := false
	for _, m := range members {
		if m.Role != role {
			continue
		}
		if m.Self {
			continue
		}
		any = true
		if m.Exited() {
			continue
		}
		out = append(out, m)
	}
	switch {
	case len(out) > 0:
		return out, nil
	case any:
		return nil, fmt.Errorf("every pane with the role %q has exited", role)
	}
	return nil, noSuchRole{role}
}

// Sender says who a message came from.
type Sender struct {
	Pane string `json:"pane,omitempty"`
	Role string `json:"role,omitempty"`
	Name string `json:"name"`
}

// Message is one message in a pane's inbox.
type Message struct {
	ID   int       `json:"id"`
	From Sender    `json:"from"`
	To   string    `json:"to"`
	Time time.Time `json:"time"`
	Text string    `json:"text"`
	Read bool      `json:"read,omitempty"`
}

// System reports that Flockdeck wrote the message, not another agent.
func (m Message) System() bool { return m.From.Pane == "" && m.From.Name == System }

// FromLabel is the sender as one phrase: its name, and its role if it has one.
func (m Message) FromLabel() string {
	if m.From.Role != "" {
		return fmt.Sprintf("%s (%s)", m.From.Name, m.From.Role)
	}
	return m.From.Name
}
