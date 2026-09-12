// Package transcript reads what agents said.
//
// Every agent that keeps a record of its conversations keeps it somewhere of
// its own and in a shape of its own, and three parts of Flockdeck want to read it:
// restoring a layout, which resumes a conversation only when there is one to
// resume; the history overlay, which lists them; and a fan-out, which takes
// the plan an agent just wrote out of its last few replies rather than off the
// screen. Each of those asks a Reader, so an agent Flockdeck has never heard of
// costs them nothing: it gets the null reader and they all cope.
package transcript

import (
	"os"
	"slices"
	"sort"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// Conversation is a stored conversation that can be resumed.
type Conversation struct {
	// ID is the session id, which is also the transcript's file name and what
	// the agent is given to reattach to it.
	ID string
	// Agent is the id of the agent that held the conversation. The history
	// overlay draws several agents' conversations in one list, and which agent
	// a row belongs to decides what happens when it is opened, so a row that
	// does not say is a row that cannot be resumed correctly.
	Agent   string
	Cwd     string
	Summary string
	// Title is the name the agent gave the conversation, when it gave it one.
	// It is what the summary falls back to, and what tells apart conversations
	// that were opened with the same prompt.
	Title    string
	Modified time.Time
	Messages int
	Size     int64
}

// NoPrompt stands in for the summary of a conversation that says nothing
// about itself: no prompt in its opening entries, and no name from the agent
// either. It is a label for a row in a list, not a name for anything.
const NoPrompt = "(no prompt recorded)"

// Reader finds and reads one agent's stored conversations.
type Reader interface {
	Path(spec agent.Spec, sessionID string) string // "" when unknown
	Replies(spec agent.Spec, sessionID string, n int) []string
	Conversations(spec agent.Spec, cwd string) ([]Conversation, error)
}

// For returns the reader for an agent.
//
// An agent that does not record what it said, or records it somewhere nobody
// here knows how to read, gets the null reader rather than a guess. Inventing
// a path for it would be worse than admitting there is none: a fan-out would
// draw its plan from a file that is not the conversation, and a restored pane
// would be told to resume something that is not there and die on the spot.
func For(spec agent.Spec) Reader {
	if !spec.Caps.Transcript {
		return Null{}
	}
	// Every API agent is the same program -- Flockdeck's own chat client -- so the
	// shape of its record is the same whichever endpoint it was talking to.
	if spec.Runner == agent.RunnerAPI {
		return Chat{}
	}
	if r, ok := readers[spec.ID]; ok {
		return r
	}
	return Null{}
}

// readers are the CLI agents whose stored conversations Flockdeck can read, by id.
// A CLI claiming Caps.Transcript that is not in here is one whose format
// nobody has written a reader for yet, and it is treated as having none.
var readers = map[string]Reader{
	"claude": Claude{},
}

// Null is the reader for an agent that records nothing Flockdeck can read.
//
// It answers every question with "there is none", which is the answer each
// caller already has to cope with: a Claude pane that has not been prompted
// yet has no transcript either, so nothing downstream is new code.
type Null struct{}

func (Null) Path(agent.Spec, string) string                           { return "" }
func (Null) Replies(agent.Spec, string, int) []string                 { return nil }
func (Null) Conversations(agent.Spec, string) ([]Conversation, error) { return nil, nil }

// Exists reports whether an agent has a stored conversation worth resuming.
//
// The file existing is not enough. A session interrupted before it recorded
// anything leaves an empty one behind, and an agent asked to resume that
// refuses and exits -- which, on a restored layout, is every such pane dying
// at once.
func Exists(spec agent.Spec, sessionID string) bool {
	if !spec.Caps.Resume {
		return false
	}
	path := For(spec).Path(spec, sessionID)
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

// Agents is the catalog the history overlay lists conversations for: every
// agent, hidden ones included, since a hidden agent's conversations are still
// there to resume.
//
// It was Claude alone until whoever assembled the catalog pointed it there,
// and nothing ever did, so an API agent's chats were recorded and never
// offered. It is still a variable, so a test can put a catalog of its own
// behind it.
var Agents = func() []agent.Spec { return agent.Load().Specs }

// All lists the stored conversations of every given agent for a working
// directory, most recently used first, each labelled with the agent that held
// it.
//
// The error is the first an agent reported, and it comes back beside whatever
// the others found: one agent whose store cannot be read is a thing to say,
// not a reason to tell somebody that the conversations they know they had are
// gone.
func All(specs []agent.Spec, cwd string) ([]Conversation, error) {
	var out []Conversation
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	var api []agent.Spec
	for _, spec := range specs {
		r := For(spec)
		// Every API agent is the same chat client writing to the same folder,
		// so the folder is read once and each chat put under the agent that
		// held it, rather than listed again under every one of them.
		if _, chat := r.(Chat); chat {
			api = append(api, spec)
			continue
		}
		found, err := r.Conversations(spec, cwd)
		note(err)
		out = append(out, found...)
	}
	if len(api) > 0 {
		rows, err := chatRows(cwd)
		note(err)
		for _, r := range rows {
			r.Agent = heldBy(r, api)
			out = append(out, r.Conversation)
		}
	}
	// Across agents as within one: most recently used first, with the id
	// breaking a tie so that two conversations started together do not swap
	// places between refreshes.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Modified.Equal(out[j].Modified) {
			return out[i].Modified.After(out[j].Modified)
		}
		return out[i].ID < out[j].ID
	})
	return out, first
}

// heldBy names the API agent a chat belongs to: the one it recorded, if it
// recorded one still in the catalog; failing that, one offering the model that
// answered in it; failing that, the first. Resuming a chat through the wrong
// agent asks another endpoint to carry on a conversation it never had, so
// every clue the file holds is used.
//
// A chat that recorded no model is a clue too. The chat client records the
// model it asked for, and a pane only asks for none when its agent has no
// default model to ask for -- a local endpoint left to choose for itself --
// so such a chat goes to an agent without one before it goes to the first.
func heldBy(r chatRow, api []agent.Spec) string {
	for _, s := range api {
		if r.agent != "" && s.ID == r.agent {
			return s.ID
		}
	}
	for _, s := range api {
		if r.model == "" && s.DefaultModel == "" {
			return s.ID
		}
		if r.model != "" && (s.DefaultModel == r.model || slices.ContainsFunc(s.Models, func(m agent.Model) bool { return m.ID == r.model })) {
			return s.ID
		}
	}
	return api[0].ID
}

// label puts the agent's id on every row a reader found. Readers are written
// against one agent's storage and have no reason to care which entry in the
// catalog sent them there, so they are spared remembering to do it.
func label(id string, convs []Conversation) []Conversation {
	for i := range convs {
		convs[i].Agent = id
	}
	return convs
}
