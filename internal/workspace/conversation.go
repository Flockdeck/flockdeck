package workspace

import "github.com/jmwri/flockdeck/internal/agent"

// conversationOf is the id of the conversation a pane is in: the one its
// agent last reported moving to, or else the pane's own id, which names the
// conversation it was started in.
//
// A pane is known by its id for as long as it lives — hook events, settings,
// the layout all find it by that — but the agent in it need not stay in one
// conversation: Claude Code starts a new one, under a new id, on /clear.
// Everything that looks the conversation up again goes through here.
func (w *Workspace) conversationOf(p *Pane) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if p.Conversation != "" {
		return p.Conversation
	}
	return p.ID
}

// ConversationOf reports the conversation a pane is in, for a caller outside
// the package: the history panel marks a conversation as open by the
// conversation a pane is in, not by the id the pane was started under. It is
// "" for a pane that is not open.
func (w *Workspace) ConversationOf(paneID string) string {
	p := w.Pane(paneID)
	if p == nil {
		return ""
	}
	return w.conversationOf(p)
}

// PaneAgentSpec resolves the agent a pane runs, the same way starting or
// restarting it does: the pane's own choice, or the project's default when it
// recorded none. It must run on the workspace goroutine, since the default
// depends on the project a pane not naming one belongs to.
//
// A shell pane answers false, the same as one that does not exist: it runs no
// agent, so resolving its empty Agent field would otherwise hand back the
// project's default agent's spec, as though the shell were running it.
//
// This is what the phone's chat view uses to find which transcript adapter,
// if any, a pane's conversation should be read with -- never a path or
// session id the client sends, only the pane id it already had reason to see.
func (w *Workspace) PaneAgentSpec(paneID string) (agent.Spec, bool) {
	p := w.Pane(paneID)
	if p == nil || !p.IsAgent() {
		return agent.Spec{}, false
	}
	return w.specFor(p.Root, p.Agent)
}
