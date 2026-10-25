package changes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Review comments (docs/changes-api.md, section 5). A comment is on one line of
// one side of a file at one head, is sent to the pane that owns the file, and is
// kept in the config directory beside the reviews.

// Limits of the comment store.
const (
	MaxComment        = 4096
	MaxBranchComments = 500
	MaxComments       = 5000
	commentFileLimit  = 8 << 20
)

// ErrTooManyComments is returned when a branch already has MaxBranchComments, or
// the store has MaxComments and none of them is closed.
var ErrTooManyComments = errors.New("there are too many comments; resolve some first")

// Comment is one comment, in the shape of the schema's comment.
type Comment struct {
	ID         string `json:"id"`
	Pane       string `json:"pane"`
	Path       string `json:"path"`
	Side       string `json:"side"`
	Line       int    `json:"line"`
	AtSha      string `json:"atSha"`
	Text       string `json:"text"`
	Author     string `json:"author"`
	To         string `json:"to"`
	Delivery   string `json:"delivery,omitempty"`
	State      string `json:"state"`
	CreatedAt  string `json:"createdAt"`
	FixedAtSha string `json:"fixedAtSha,omitempty"`
}

// commentRecord is a comment as the store keeps it: with the branch it is of, so
// that it survives the pane's id, and the id of the message that carried it, so
// that its delivery can be read from the inbox.
type commentRecord struct {
	Branch string `json:"branch"`
	MsgID  int    `json:"msgId,omitempty"`
	Comment
}

// CommentState is the file of comments. Only Flockdeck writes it.
type CommentState struct {
	Next     int             `json:"next"`
	Comments []commentRecord `json:"comments"`
}

var commentIDRe = regexp.MustCompile(`^c[0-9]{1,9}$`)

func validCommentState(s string) bool {
	switch s {
	case "open", "fixed", "resolved", "dismissed":
		return true
	}
	return false
}

// LoadComments reads the store at path and cleans it, as LoadReviews does: a
// missing file is an empty store, one that cannot be read or is not valid is an
// error with a copy kept beside it, and entries with a bad shape are dropped.
func LoadComments(path string) (CommentState, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return CommentState{}, nil
	}
	if err != nil {
		return CommentState{}, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(f, commentFileLimit+1)); err != nil {
		return CommentState{}, err
	}
	var raw CommentState
	if buf.Len() > commentFileLimit || json.Unmarshal(buf.Bytes(), &raw) != nil {
		kept := ""
		if buf.Len() <= commentFileLimit {
			kept = keepBadCopy(path, buf.Bytes())
		}
		if kept == "" {
			return CommentState{}, errors.New("the comment file is damaged; no copy of it could be kept")
		}
		return CommentState{}, errors.New("the comment file is damaged; a copy is kept as " + kept)
	}
	out := CommentState{Next: raw.Next}
	seen := map[string]bool{}
	for _, r := range raw.Comments {
		r.Branch = cleanLine(r.Branch, 256)
		r.Pane = cleanLine(r.Pane, 64)
		r.Path = cleanLine(r.Path, 1024)
		r.Author = cleanLine(r.Author, 64)
		r.To = cleanLine(r.To, 64)
		r.Text = CleanText(r.Text, MaxComment)
		r.Delivery = ""
		if r.State != "fixed" {
			r.FixedAtSha = ""
		}
		if !commentIDRe.MatchString(r.ID) || seen[r.ID] || r.Branch == "" || r.Pane == "" || r.Path == "" || r.Author == "" || r.To == "" || r.Text == "" ||
			(r.Side != "old" && r.Side != "new") || r.Line < 0 || !shaRe.MatchString(r.AtSha) || !validCommentState(r.State) {
			continue
		}
		if _, err := time.Parse(time.RFC3339, r.CreatedAt); err != nil {
			continue
		}
		if r.FixedAtSha != "" && !shaRe.MatchString(r.FixedAtSha) {
			r.FixedAtSha = ""
		}
		if n, err := strconv.Atoi(r.ID[1:]); err == nil && n >= out.Next {
			out.Next = n + 1
		}
		seen[r.ID] = true
		out.Comments = append(out.Comments, r)
		if len(out.Comments) == MaxComments {
			break
		}
	}
	return out, nil
}

// Save writes the store atomically.
func (s CommentState) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// Add records c as a comment of branch, carried by message msgID, and returns it
// with its id. It is open, and its Delivery is not kept. A branch keeps at most
// MaxBranchComments. When the store holds MaxComments, the oldest resolved or
// dismissed comment is dropped to make room; if there is none, nothing is
// recorded.
func (s *CommentState) Add(branch string, c Comment, msgID int) (Comment, error) {
	n := 0
	for _, r := range s.Comments {
		if r.Branch == branch {
			n++
		}
	}
	if n >= MaxBranchComments {
		return Comment{}, ErrTooManyComments
	}
	if len(s.Comments) >= MaxComments {
		drop := -1
		for i, r := range s.Comments {
			if r.State == "resolved" || r.State == "dismissed" {
				if drop < 0 || r.CreatedAt < s.Comments[drop].CreatedAt {
					drop = i
				}
			}
		}
		if drop < 0 {
			return Comment{}, ErrTooManyComments
		}
		s.Comments = append(s.Comments[:drop], s.Comments[drop+1:]...)
	}
	c.Text = CleanText(c.Text, MaxComment)
	c.State, c.Delivery, c.FixedAtSha = "open", "", ""
	if s.Next < 1 {
		s.Next = 1
	}
	c.ID = "c" + strconv.Itoa(s.Next)
	s.Next++
	s.Comments = append(s.Comments, commentRecord{Branch: branch, MsgID: msgID, Comment: c})
	return c, nil
}

// ErrNoComment is returned for an id the store does not have on that branch.
var ErrNoComment = errors.New("no such comment")

// Resolve closes the comment of branch: an open or fixed comment becomes
// resolved. A comment that is already closed is returned as it is.
func (s *CommentState) Resolve(branch, id string) (Comment, error) {
	return s.move(branch, id, func(c *Comment) {
		if c.State == "open" || c.State == "fixed" {
			c.State, c.FixedAtSha = "resolved", ""
		}
	})
}

// Reopen makes a comment of branch open again, whatever it was.
func (s *CommentState) Reopen(branch, id string) (Comment, error) {
	return s.move(branch, id, func(c *Comment) { c.State, c.FixedAtSha = "open", "" })
}

func (s *CommentState) move(branch, id string, f func(*Comment)) (Comment, error) {
	for i := range s.Comments {
		if s.Comments[i].Branch == branch && s.Comments[i].ID == id {
			f(&s.Comments[i].Comment)
			return s.Comments[i].Comment, nil
		}
	}
	return Comment{}, ErrNoComment
}

// For lists the comments of branch, oldest first, as the pane now called pane has
// them, with delivery read from deliveries by the message id each was sent as
// (the empty string for a message that is not in the map).
func (s CommentState) For(branch, pane string, delivery func(to string, msgID int) string) []Comment {
	out := []Comment{}
	for _, r := range s.Comments {
		if r.Branch != branch {
			continue
		}
		c := r.Comment
		c.Pane = pane
		if delivery != nil && r.MsgID != 0 {
			c.Delivery = delivery(r.To, r.MsgID)
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// HasComments says whether any comment is kept for a message to look up.
func (s CommentState) HasComments() bool { return len(s.Comments) > 0 }

// SideLines is the number of lines the file at path has at the commit rev, and
// whether it is there at all. A file too large to read is an error.
func SideLines(ctx context.Context, cwd, rev, path string) (lines int, exists bool, err error) {
	if _, err := git(ctx, cwd, "cat-file", "-e", rev+":"+path); err != nil {
		return 0, false, nil
	}
	out, err := git(ctx, cwd, "cat-file", "blob", rev+":"+path)
	if err != nil {
		return 0, true, err
	}
	if out == "" {
		return 0, true, nil
	}
	n := strings.Count(out, "\n")
	if !strings.HasSuffix(out, "\n") {
		n++
	}
	return n, true, nil
}
