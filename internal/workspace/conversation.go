package workspace

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
