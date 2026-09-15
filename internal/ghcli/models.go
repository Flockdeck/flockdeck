package ghcli

import "time"

// Actor is whoever opened or wrote something -- a PR, an issue, a comment --
// as gh's --json reports it. Only Login is ever shown; the rest of what gh
// sends (id, whether they are a bot, their display name) is decoded past and
// dropped, the same as any field of gh's JSON this package has not asked to
// keep.
type Actor struct {
	Login string `json:"login"`
}

// Comment is one comment on a PR or an issue.
type Comment struct {
	Author    Actor     `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	URL       string    `json:"url"`
}

// PR is a pull request, as much of it as the panel shows.
type PR struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"` // OPEN, CLOSED, MERGED
	IsDraft     bool      `json:"isDraft"`
	Author      Actor     `json:"author"`
	URL         string    `json:"url"`
	HeadRefName string    `json:"headRefName"`
	BaseRefName string    `json:"baseRefName"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Comments is filled in by ViewPR; ListPRs leaves it nil, since asking
	// gh for every open PR's comments in one listing call is not something
	// --json supports doing cheaply.
	Comments []Comment `json:"comments"`
}

// listFields is what ListPRs and ListIssues ask gh for: enough for a row in
// the list, and nothing that would need a second call per row.
const listFields = "number,title,state,author,url,createdAt,updatedAt"

// prListFields adds the pull-request-only columns to listFields.
const prListFields = listFields + ",isDraft,headRefName,baseRefName"

// viewFields adds the body and comments, which only a single item's own view
// needs and a whole list would be expensive to carry.
const viewFields = listFields + ",body,comments"

const prViewFields = prListFields + ",body,comments"

// Issue is an issue, as much of it as the panel shows.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"` // OPEN, CLOSED
	Author    Actor     `json:"author"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Comments  []Comment `json:"comments"`
}
