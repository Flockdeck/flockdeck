package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/changes"
)

// Review comments (docs/changes-api.md, section 5): commentAdd, commentResolve and
// commentReopen. A comment is kept in changes/<project>-<8 hex>.comments.json,
// beside the reviews, and goes to the pane that owns the file as a message from
// the person at the window. The comments use reviewsMu, the lock of the review
// state, so that one lock orders the two files.

// commentsPath is the comment store beside the review state at statePath.
func commentsPath(statePath string) string {
	return strings.TrimSuffix(statePath, ".state.json") + ".comments.json"
}

// loadComments loads the comment store at path, with reviewsMu held. A damaged
// file is an empty store and one notice to the windows until it loads again.
func (s *Server) loadComments(path string) changes.CommentState {
	st, err := changes.LoadComments(path)
	if firstDamage(path, err) {
		s.notifyAllOr("Comments are not shown: "+err.Error(), "The comment file is damaged; comments are not shown", true)
	}
	return st
}

// commentDeliveries reads the delivery state of the messages that carried comments
// from the inbox, once for a push. It returns nil when there is no comment or the
// inbox cannot be read, and a comment then shows no delivery.
func (s *Server) commentDeliveries(root string) func(to string, id int) string {
	type key struct {
		to string
		id int
	}
	got, ok := ask(s, func() map[key]string {
		msgs, _, err := s.ws.TeamMessages(root)
		if err != nil {
			return nil
		}
		m := make(map[key]string, len(msgs))
		for _, msg := range msgs {
			m[key{msg.To, msg.ID}] = msg.Delivery()
		}
		return m
	})
	if !ok || got == nil {
		return nil
	}
	return func(to string, id int) string { return got[key{to, id}] }
}

// commentText is the message a comment is sent as. It names the branch, the file
// and the line, and quotes the comment.
func commentText(branch, path, side string, line int, sha, text string) string {
	where := "on the whole file"
	if line > 0 {
		where = fmt.Sprintf("on line %d (%s side)", line, side)
	}
	return fmt.Sprintf("Review comment on %s, in %s %s, at %s. Quoted, from the person at the window:\n%s\nFix it and commit. The person closes comments; a comment you have dealt with stays open until they do.",
		changes.CleanLine(branch, 200), changes.CleanLine(path, 400), where, shortSha(sha), indent(changes.CleanText(text, changes.MaxComment)))
}

// commentWork is what a comment command needs once its pane is found: the
// checkout and its branch as read, the store, and the way to send a message to the
// pane that owns the file.
type commentWork struct {
	pane, cwd string
	branch    changes.Branch
	store     string
	send      func(to, text string) (msgID int, err error)
}

// commentRefusal is a refusal that a handler turns into a changesResult.
type commentRefusal struct{ code, msg string }

// addComment is commentAdd once the pane is known. The message is sent only when
// everything that can be checked first has passed, and the comment is recorded
// only when the message was sent.
func addComment(ctx context.Context, w commentWork, cmd command) (changes.Comment, *commentRefusal) {
	if cmd.File == "" {
		return changes.Comment{}, &commentRefusal{"badRequest", "id and file are required"}
	}
	if cmd.Side != "old" && cmd.Side != "new" {
		return changes.Comment{}, &commentRefusal{"badRequest", "side is old or new"}
	}
	if cmd.Line < 0 {
		return changes.Comment{}, &commentRefusal{"badRequest", "line is 0 for the whole file, or the line number from 1"}
	}
	if cmd.ExpectSha == "" {
		return changes.Comment{}, &commentRefusal{"badRequest", "expectSha is required"}
	}
	text := strings.TrimSpace(changes.CleanText(cmd.Text, changes.MaxComment+1))
	if text == "" {
		return changes.Comment{}, &commentRefusal{"badRequest", "text is required"}
	}
	if utf8.RuneCountInString(text) > changes.MaxComment {
		return changes.Comment{}, &commentRefusal{"badRequest", "the comment is too long"}
	}
	b := w.branch
	if !sameSha(cmd.ExpectSha, b.HeadSha) {
		return changes.Comment{}, &commentRefusal{"headMoved", fmt.Sprintf("%s has a new commit since you looked, so nothing was added. Look at it again.", b.Branch)}
	}
	_, f, ref := changes.BranchFile(ctx, w.pane, w.cwd, cmd.File)
	if ref != nil {
		return changes.Comment{}, &commentRefusal{ref.Code, ref.Msg}
	}
	if cmd.Line > 0 {
		rev, path := b.HeadSha, f.Path
		if cmd.Side == "old" {
			rev, path = b.BaseSha, f.Path
			if f.OldPath != "" {
				path = f.OldPath
			}
		}
		n, exists, err := changes.SideLines(ctx, w.cwd, rev, path)
		switch {
		case rev == "" || (err == nil && !exists):
			return changes.Comment{}, &commentRefusal{"badRequest", "the file has no " + cmd.Side + " side to comment on"}
		case err != nil:
			return changes.Comment{}, &commentRefusal{"badRequest", "the file could not be read to check the line"}
		case cmd.Line > n:
			return changes.Comment{}, &commentRefusal{"badRequest", fmt.Sprintf("the %s side of the file has %d lines", cmd.Side, n)}
		}
	}
	to := f.Owner
	if to == "" {
		to = w.pane
	}
	// The store is read before the message is sent: a damaged store means nothing is
	// sent that could not be recorded.
	reviewsMu.Lock()
	defer reviewsMu.Unlock()
	st, err := changes.LoadComments(w.store)
	if err != nil {
		return changes.Comment{}, &commentRefusal{"badRequest", err.Error()}
	}
	msgID, err := w.send(to, commentText(b.Branch, f.Path, cmd.Side, cmd.Line, b.HeadSha, text))
	if err != nil {
		return changes.Comment{}, &commentRefusal{"badRequest", err.Error()}
	}
	added, err := st.Add(b.Branch, changes.Comment{Pane: w.pane, Path: f.Path, Side: cmd.Side, Line: cmd.Line, AtSha: b.HeadSha, Text: text,
		Author: "you", To: to, CreatedAt: time.Now().UTC().Format(time.RFC3339)}, msgID)
	if err == nil {
		err = st.Save(w.store)
	}
	if err != nil {
		return changes.Comment{}, &commentRefusal{"badRequest", "the comment was sent but could not be recorded: " + err.Error()}
	}
	return added, nil
}

// moveComment is commentResolve and commentReopen once the pane is known.
func moveComment(w commentWork, cmd command) (changes.Comment, *commentRefusal) {
	if cmd.CommentID == "" {
		return changes.Comment{}, &commentRefusal{"badRequest", "id and commentId are required"}
	}
	reviewsMu.Lock()
	defer reviewsMu.Unlock()
	st, err := changes.LoadComments(w.store)
	if err != nil {
		return changes.Comment{}, &commentRefusal{"badRequest", err.Error()}
	}
	var moved changes.Comment
	if cmd.Cmd == "commentResolve" {
		moved, err = st.Resolve(w.branch.Branch, cmd.CommentID)
	} else {
		moved, err = st.Reopen(w.branch.Branch, cmd.CommentID)
	}
	if err == changes.ErrNoComment {
		return changes.Comment{}, &commentRefusal{"notFound", "no such comment on this branch"}
	}
	if err == nil {
		err = st.Save(w.store)
	}
	if err != nil {
		return changes.Comment{}, &commentRefusal{"badRequest", err.Error()}
	}
	moved.Pane = w.pane
	return moved, nil
}

// commentWorkOf finds the pane of a comment command and what the command needs.
func (s *Server) commentWorkOf(c *controlClient, cmd command) (commentWork, bool) {
	cwd, b, _, ok := s.checkPane(c, cmd)
	if !ok {
		return commentWork{}, false
	}
	root, project, ok := s.projectOf(cmd.ID)
	if !ok {
		changesRefuse(c, cmd, "noBranch", "no such pane")
		return commentWork{}, false
	}
	_, statePath, err := changesFiles(root, project)
	if err != nil {
		changesRefuse(c, cmd, "badRequest", "the config directory cannot be found")
		return commentWork{}, false
	}
	send := func(to, text string) (int, error) {
		var id int
		var sendErr error
		if _, ok := ask(s, func() bool {
			res, err := s.ws.UserMessage(root, to, text)
			switch {
			case err != nil:
				sendErr = err
			case len(res.Sent) != 1:
				sendErr = fmt.Errorf("the comment could not be delivered to %s", to)
			default:
				id = res.Sent[0].ID
			}
			return true
		}); !ok {
			return 0, errors.New("Flockdeck is closing")
		}
		return id, sendErr
	}
	return commentWork{pane: cmd.ID, cwd: cwd, branch: b, store: commentsPath(statePath), send: send}, true
}

// changesCommentAdd answers commentAdd.
func (s *Server) changesCommentAdd(c *controlClient, cmd command) {
	if cmd.ID == "" {
		changesRefuse(c, cmd, "badRequest", "id and file are required")
		return
	}
	w, ok := s.commentWorkOf(c, cmd)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesReadLimit)
	defer cancel()
	added, ref := addComment(ctx, w, cmd)
	if ref != nil {
		changesRefuse(c, cmd, ref.code, ref.msg)
		return
	}
	c.sendJSON(changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Comment: &added})
}

// changesCommentMove answers commentResolve and commentReopen.
func (s *Server) changesCommentMove(c *controlClient, cmd command) {
	if cmd.ID == "" || cmd.CommentID == "" {
		changesRefuse(c, cmd, "badRequest", "id and commentId are required")
		return
	}
	w, ok := s.commentWorkOf(c, cmd)
	if !ok {
		return
	}
	moved, ref := moveComment(w, cmd)
	if ref != nil {
		changesRefuse(c, cmd, ref.code, ref.msg)
		return
	}
	c.sendJSON(changesResultMsg{Type: "changesResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Comment: &moved})
}
