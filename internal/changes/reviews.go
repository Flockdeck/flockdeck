package changes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Limits of the review state.
const (
	MaxNote       = 2000
	MaxReviews    = 200
	MaxReviewer   = 64
	reviewStateMB = 4 << 20
)

// ErrReviewsFull is returned by Set when the state holds MaxReviews reviews that
// may not be dropped.
var ErrReviewsFull = errors.New("there are too many reviews to keep another")

// Review is one review of a branch, in the shape of the schema's review.
type Review struct {
	Reviewer string `json:"reviewer"`
	State    string `json:"state"`
	Note     string `json:"note,omitempty"`
	At       string `json:"at"`
	AtSha    string `json:"atSha"`
	Stale    bool   `json:"stale,omitempty"`
}

// reviewRecord is a review as the state file keeps it, with the branch it is
// of. Branch is the pane's branch name, so a review survives the pane's id.
type reviewRecord struct {
	Branch string `json:"branch"`
	Review
}

// ReviewState is the file of reviews. Only Flockdeck writes it.
type ReviewState struct {
	Reviews []reviewRecord `json:"reviews"`
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func validState(s string) bool {
	switch s {
	case "requested", "approved", "changes", "dismissed":
		return true
	}
	return false
}

// LoadReviews reads the state file at path and cleans it: unknown fields are
// dropped by decoding into the known shape and writing back only that, entries
// over the limits are cut, entries with an unknown state or a malformed sha are
// dropped.
//
// A file that is not there is an empty state and no error. One that is there and
// cannot be read, is too large or is not valid is an error, and a copy of it is
// kept beside it as <path>.bad, so a caller that refuses to save over it does not
// lose the person's reviews to a bad write.
func LoadReviews(path string) (ReviewState, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return ReviewState{}, nil
	}
	if err != nil {
		return ReviewState{}, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(f, reviewStateMB+1)); err != nil {
		return ReviewState{}, err
	}
	var raw ReviewState
	if buf.Len() > reviewStateMB || json.Unmarshal(buf.Bytes(), &raw) != nil {
		if buf.Len() <= reviewStateMB {
			keepBadCopy(path, buf.Bytes())
		}
		return ReviewState{}, errors.New("the review file is damaged; a copy was kept as " + path + ".bad")
	}
	var out ReviewState
	for _, r := range raw.Reviews {
		r.Branch = cleanLine(r.Branch, 256)
		r.Reviewer = cleanLine(r.Reviewer, MaxReviewer)
		r.Note = CleanNote(r.Note)
		r.Stale = false
		if r.Branch == "" || r.Reviewer == "" || !validState(r.State) || !shaRe.MatchString(r.AtSha) {
			continue
		}
		if _, err := time.Parse(time.RFC3339, r.At); err != nil {
			continue
		}
		out.Reviews = append(out.Reviews, r)
	}
	sort.SliceStable(out.Reviews, func(i, j int) bool { return out.Reviews[i].At > out.Reviews[j].At })
	if len(out.Reviews) > MaxReviews {
		out.Reviews = out.Reviews[:MaxReviews]
	}
	return out, nil
}

// keepBadCopy saves a damaged file beside it as <path>.bad. A copy that is already
// there with other content is not written over: the next free <path>.bad.N is used
// (up to nine), and a copy with the same content is not written again.
func keepBadCopy(path string, data []byte) {
	for i := 0; i < 10; i++ {
		name := path + ".bad"
		if i > 0 {
			name += "." + strconv.Itoa(i)
		}
		old, err := os.ReadFile(name)
		if errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(name, data, 0o600)
			return
		}
		if err == nil && bytes.Equal(old, data) {
			return
		}
	}
}

// CleanNote keeps the line breaks and tabs of a note and drops every other
// control, format and line or paragraph separator character, then cuts it to
// MaxNote characters.
func CleanNote(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= MaxNote {
			break
		}
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)) {
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// Save writes the state atomically: a temporary file in the same directory,
// then a rename over the old one.
func (s ReviewState) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".reviews-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// LiveBranches lists the local branches of the repository at cwd with one git
// call. A caller that cannot get the list must treat no branch as gone.
func LiveBranches(ctx context.Context, cwd string) (map[string]bool, error) {
	out, err := git(ctx, cwd, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			live[l] = true
		}
	}
	return live, nil
}

// Set records a review of branch by reviewer, replacing that reviewer's earlier
// one of the branch.
//
// When the state is full, reviews are dropped in this order, oldest first within
// each step: requests and dismissals of branches that are gone, requests, then
// dismissals, then anything else of a branch that is gone. The person's approved
// and changes reviews of a branch that still exists are never dropped; if nothing
// else can go, Set returns ErrReviewsFull and records nothing. gone says whether a
// branch no longer exists; nil means none is.
func (s *ReviewState) Set(branch string, r Review, gone func(branch string) bool) error {
	r.Reviewer = cleanLine(r.Reviewer, MaxReviewer)
	r.Note = CleanNote(r.Note)
	r.Stale = false
	kept := make([]reviewRecord, 0, len(s.Reviews)+1)
	kept = append(kept, reviewRecord{Branch: branch, Review: r})
	for _, x := range s.Reviews {
		if x.Branch == branch && x.Reviewer == r.Reviewer {
			continue
		}
		kept = append(kept, x)
	}
	isGone := func(x reviewRecord) bool { return gone != nil && x.Branch != branch && gone(x.Branch) }
	steps := []func(reviewRecord) bool{
		func(x reviewRecord) bool { return isGone(x) && (x.State == "requested" || x.State == "dismissed") },
		func(x reviewRecord) bool { return x.State == "requested" && x != kept[0] },
		func(x reviewRecord) bool { return x.State == "dismissed" && x != kept[0] },
		func(x reviewRecord) bool { return isGone(x) },
	}
	// kept is newest first (the new review is first, the rest in file order), so
	// the oldest candidates are at the end.
	for _, drop := range steps {
		for i := len(kept) - 1; i > 0 && len(kept) > MaxReviews; i-- {
			if drop(kept[i]) {
				kept = append(kept[:i], kept[i+1:]...)
			}
		}
	}
	if len(kept) > MaxReviews {
		return ErrReviewsFull
	}
	s.Reviews = kept
	return nil
}

// For lists the reviews of branch, marking those made at another head stale.
func (s ReviewState) For(branch, headSha string) []Review {
	out := []Review{}
	for _, r := range s.Reviews {
		if r.Branch != branch {
			continue
		}
		rv := r.Review
		rv.Stale = headSha != "" && !strings.HasPrefix(headSha, rv.AtSha) && !strings.HasPrefix(rv.AtSha, headSha)
		out = append(out, rv)
	}
	return out
}
