// Package changes reads the state of agents' checkouts for the team-wide Changes
// view (docs/changes-api.md): for each checkout its branch, its files against the
// base, and what the pane has not yet committed. It reads and never writes.
package changes

// LastCommit is the newest commit on a branch.
type LastCommit struct {
	Sha     string `json:"sha"`
	Subject string `json:"subject"`
	At      string `json:"at"`
}

// ConflictWith names another branch that changes the same path.
type ConflictWith struct {
	Pane string `json:"pane"`
	Path string `json:"path"`
}

// File is one changed file of a branch, in the shape of the schema's file.
type File struct {
	Path         string         `json:"path"`
	OldPath      string         `json:"oldPath,omitempty"`
	Status       string         `json:"status"`
	Added        int            `json:"added"`
	Removed      int            `json:"removed"`
	Binary       bool           `json:"binary,omitempty"`
	SizeBefore   *int64         `json:"sizeBefore,omitempty"`
	SizeAfter    *int64         `json:"sizeAfter,omitempty"`
	Similarity   int            `json:"similarity,omitempty"`
	Generated    bool           `json:"generated,omitempty"`
	ModeChange   string         `json:"modeChange,omitempty"`
	Staged       bool           `json:"staged,omitempty"`
	Owner        string         `json:"owner,omitempty"`
	ConflictWith []ConflictWith `json:"conflictWith,omitempty"`
}

// Branch is one pane's checkout. When Error is set only Pane, Cwd and Error are.
// Checks, Reviews and Comments are pointers to empty lists until those parts are
// built, so an unreadable checkout omits them and a read one writes [].
type Branch struct {
	Pane        string      `json:"pane"`
	Cwd         string      `json:"cwd"`
	Branch      string      `json:"branch,omitempty"`
	Detached    bool        `json:"detached,omitempty"`
	Base        string      `json:"base,omitempty"`
	BaseSha     string      `json:"baseSha,omitempty"`
	HeadSha     string      `json:"headSha,omitempty"`
	Upstream    string      `json:"upstream,omitempty"`
	Ahead       *int        `json:"ahead,omitempty"`
	Behind      *int        `json:"behind,omitempty"`
	Uncommitted *int        `json:"uncommitted,omitempty"`
	Untracked   *int        `json:"untracked,omitempty"`
	LastCommit  *LastCommit `json:"lastCommit,omitempty"`
	TaskID      string      `json:"taskId,omitempty"`
	Files       []File      `json:"files,omitempty"`
	Omitted     int         `json:"omitted,omitempty"`
	Checks      *[]Check    `json:"checks,omitempty"`
	Reviews     *[]Review   `json:"reviews,omitempty"`
	Comments    *[]Comment  `json:"comments,omitempty"`
	Error       string      `json:"error,omitempty"`

	// all is every file when Files was cut to the limit, for the conflict check and
	// for filesPage.
	all []File
}

// AllFiles is every file of the branch, whether or not Files was cut.
func (b Branch) AllFiles() []File {
	if b.all != nil {
		return b.all
	}
	return b.Files
}

// Partial says that some checkouts could not be read in time.
type Partial struct {
	Read  int `json:"read"`
	Total int `json:"total"`
}

// Team is the changesTeam message.
type Team struct {
	Req      string   `json:"req,omitempty"`
	Root     string   `json:"root"`
	Project  string   `json:"project"`
	Rev      int      `json:"rev"`
	Branches []Branch `json:"branches"`
	Partial  *Partial `json:"partial,omitempty"`
}

// Files is the changesFiles message.
type Files struct {
	Req    string `json:"req,omitempty"`
	Pane   string `json:"pane"`
	Offset int    `json:"offset"`
	Total  int    `json:"total"`
	Files  []File `json:"files"`
}
