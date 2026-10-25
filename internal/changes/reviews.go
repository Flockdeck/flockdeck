package changes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Limits of the review state.
const (
	MaxNote       = 2000
	MaxReviews    = 200
	MaxReviewer   = 64
	reviewStateMB = 4 << 20
)

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
// over the limits are cut, entries with an unknown state or a malformed sha
// are dropped. A missing or unreadable file is an empty state.
func LoadReviews(path string) ReviewState {
	f, err := os.Open(path)
	if err != nil {
		return ReviewState{}
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(f, reviewStateMB+1)); err != nil || buf.Len() > reviewStateMB {
		return ReviewState{}
	}
	var raw ReviewState
	if json.Unmarshal(buf.Bytes(), &raw) != nil {
		return ReviewState{}
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
	return out
}

// CleanNote keeps line breaks and tabs of a note and drops other control and
// format characters.
func CleanNote(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= MaxNote {
			break
		}
		switch {
		case r == '\n' || r == '\t':
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0xfeff:
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

// Set records a review of branch by reviewer, replacing that reviewer's earlier
// one of the branch.
func (s *ReviewState) Set(branch string, r Review) {
	r.Reviewer = cleanLine(r.Reviewer, MaxReviewer)
	r.Note = CleanNote(r.Note)
	r.Stale = false
	kept := s.Reviews[:0]
	for _, x := range s.Reviews {
		if x.Branch == branch && x.Reviewer == r.Reviewer {
			continue
		}
		kept = append(kept, x)
	}
	s.Reviews = append([]reviewRecord{{Branch: branch, Review: r}}, kept...)
	if len(s.Reviews) > MaxReviews {
		s.Reviews = s.Reviews[:MaxReviews]
	}
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
