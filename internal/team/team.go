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
	"unicode"
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
	// Called is what the pane is called for the work it does (the pane's label,
	// set with spawn -name or flockdeck pane-name), empty until it has one.
	Called string `json:"label,omitempty"`
	// Role is the id of the role the pane was started as, empty for none.
	Role   string `json:"role,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"`
	Status string `json:"status"`
	// Self marks the pane the roster was asked for.
	Self bool `json:"self,omitempty"`
}

// Label is what to call the member: what it is called for its work, else its
// peer name, else its pane name.
func (m Member) Label() string {
	if m.Called != "" {
		return PaneLabelText(m.Called)
	}
	if m.Peer != "" {
		return AgentLabel(m.Peer)
	}
	return AgentLabel(m.Name)
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
	fmt.Fprintf(&b, "pane %s: %s %q", m.Pane, role, m.Label())
	if m.Peer != "" && m.Name != "" && m.Peer != m.Name {
		fmt.Fprintf(&b, " (pane name %q)", m.Name)
	}
	if m.Agent != "" {
		b.WriteString(", " + m.Agent)
		if m.Model != "" {
			b.WriteString(" · " + m.Model)
		}
	}
	fmt.Fprintf(&b, ", %s", m.Status)
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

// UserName is what a message from the person at the window is signed with, and
// what the window's notes and notices call them to an agent. The window itself
// shows "you".
const UserName = "the user"

// UserLabel is how an agent is shown the person at the window: in notices, in
// `flockdeck task show` and in `flockdeck inbox`. It is chosen by the Actor's or
// Sender's User flag, never by a name, and AgentLabel rewrites any name an agent
// reports that could pass for it, so no agent can sign as the user.
const UserLabel = "the user (at the window)"

// reserved are the names an agent may not take, as Normalize writes them: the
// person at the window, and Flockdeck itself.
var reserved = []string{"theuser", "user", "you", "me", "flockdeck", "flockdecksystem", "system", "admin", "operator"}

// Normalize is a name with everything but letters and digits dropped and the
// case folded, so "The  User", "the_user" and "T.H.E U-S-E-R" are one name.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Impersonates says a name could be read as the person at the window or as
// Flockdeck: it is, once normalized, one of the reserved names or begins with
// "theuser" or "youat".
func Impersonates(name string) bool {
	n := Normalize(name)
	for _, r := range reserved {
		if n == r {
			return true
		}
	}
	return strings.HasPrefix(n, "theuser") || strings.HasPrefix(n, "youat")
}

// AgentLabel is a name an agent chose for itself, or its pane's name, as a
// label: PlainLabel, and rewritten as "(agent) name" when it could pass for the
// user or for Flockdeck. The rewrite keeps what the agent wrote and cannot be
// mistaken for either.
func AgentLabel(name string) string {
	n := PlainLabel(name)
	if Impersonates(n) {
		return PlainLabel("(agent) " + n)
	}
	return n
}

// MaxPaneLabel is the longest label a pane may have, in characters.
const MaxPaneLabel = 40

// labelRoles are the role ids a label may not pass for, besides the ids of the
// roles of the project it is given in.
var labelRoles = []string{ManagerRole, "reviewer", "planner", "tester", "worker"}

// noFormat drops the format characters (Unicode category Cf): zero-width and
// direction marks, the byte order mark and the tag block. They print as nothing
// or reorder what is around them, so a label could hide or fake words with them.
func noFormat(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// PlainName is a name that another pane chose, as text for a notice: format
// characters dropped (see noFormat), one line, at most max characters.
func PlainName(s string, max int) string { return PlainText(noFormat(s), max) }

// PaneLabelText is a pane's label as text: one line of at most MaxPaneLabel
// characters with no control or format character in it, and rewritten as
// "(agent) name" when it could pass for the user or for Flockdeck, or for a role
// (manager, reviewer, planner, tester, worker, or one of roles), as AgentLabel does.
func PaneLabelText(s string, roles ...string) string {
	n := PlainText(noFormat(s), MaxPaneLabel)
	if Impersonates(n) || passesForRole(n, roles) {
		return PlainText("(agent) "+n, MaxPaneLabel)
	}
	return n
}

func passesForRole(label string, roles []string) bool {
	n := Normalize(label)
	if n == "" {
		return false
	}
	for _, list := range [][]string{labelRoles, roles} {
		for _, r := range list {
			if n == Normalize(r) {
				return true
			}
		}
	}
	return false
}

// Sender says who a message came from.
type Sender struct {
	Pane string `json:"pane,omitempty"`
	Role string `json:"role,omitempty"`
	Name string `json:"name"`
	// User marks a message the person at the window wrote. Like the rest of the
	// sender it is not authenticated for the agent that reads it.
	User bool `json:"user,omitempty"`
}

// UserSender is the person at the window as the sender of a message. It has no
// pane, so a reply to it goes nowhere an agent can send to.
func UserSender() Sender { return Sender{Name: UserName, User: true} }

// Message is one message in a pane's inbox.
type Message struct {
	ID   int       `json:"id"`
	From Sender    `json:"from"`
	To   string    `json:"to"`
	Time time.Time `json:"time"`
	Text string    `json:"text"`
	Read bool      `json:"read,omitempty"`
	// Seen is the window's own mark, set when the person at the window says they
	// have seen the message. It is not Read: the agent's inbox, its unread count
	// and the notice that tells it of mail never look at it.
	Seen bool `json:"seen,omitempty"`
	// NotifiedAt, WokenAt and ReadAt are when the message reached each delivery
	// state (see Delivery). A zero time means it has not. A snapshot from before
	// they existed loads with all three zero, and a message that is Read but has
	// no ReadAt still counts as read.
	NotifiedAt time.Time `json:"notifiedAt,omitzero"`
	WokenAt    time.Time `json:"wokenAt,omitzero"`
	ReadAt     time.Time `json:"readAt,omitzero"`
}

// The delivery states of a message, in the order a message normally passes
// through them.
const (
	// DeliveryQueued: the message is stored in the inbox.
	DeliveryQueued = "queued"
	// DeliveryNotified: the hook notice of it was given to the agent.
	DeliveryNotified = "notified"
	// DeliveryWoken: a line was typed into the agent's pane to wake it.
	DeliveryWoken = "woken"
	// DeliveryRead: the agent ran `inbox read` for it.
	DeliveryRead = "read"
)

// Delivery is the newest state the message has reached. The states can be
// reached out of order, a wake before the notice for instance, and the newest
// is the furthest along in the order above, not the last one marked.
func (m Message) Delivery() string {
	switch {
	case m.Read || !m.ReadAt.IsZero():
		return DeliveryRead
	case !m.WokenAt.IsZero():
		return DeliveryWoken
	case !m.NotifiedAt.IsZero():
		return DeliveryNotified
	}
	return DeliveryQueued
}

// System reports that Flockdeck wrote the message, not another agent.
func (m Message) System() bool { return m.From.Pane == "" && !m.From.User && m.From.Name == System }

// FromLabel is the sender as one phrase: its name, and its role if it has one.
//
// It is made of the sender's own words, so control characters, a newline in a
// peer name among them, are replaced and the length is capped: the label is
// printed on a line of its own and must not be able to start another.
func (m Message) FromLabel() string {
	if m.From.User {
		return UserLabel
	}
	name := PlainLabel(m.From.Name)
	if m.From.Role != "" {
		return fmt.Sprintf("%s (%s)", name, PlainLabel(m.From.Role))
	}
	return name
}

// MaxLabel is the most runes a pane's peer name may have.
const MaxLabel = 64

// PlainLabel turns control characters and line separators into spaces, collapses
// runs of white space and cuts the result to MaxLabel runes. Letters of any
// script are kept.
func PlainLabel(s string) string { return PlainText(s, MaxLabel) }

// PlainText is PlainLabel with a limit of the caller's choosing: one line of at
// most max runes, with no control character in it.
func PlainText(s string, max int) string {
	var b strings.Builder
	n := 0
	space := false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) { // IsSpace covers U+2028 and U+2029
			space = b.Len() > 0
			continue
		}
		if n == max {
			break
		}
		if space {
			// A space with no room for the character after it is not written, so
			// the text never ends in one.
			if n+1 >= max {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
