package ghcli

import "strconv"

// ListPRs lists pull requests in the repository dir sits in. state is one of
// "open", "closed", "merged" or "all"; "" means "open", gh's own default.
func ListPRs(dir, state string, limit int) ([]PR, error) {
	args := []string{"pr", "list", "--json", prListFields, "--limit", strconv.Itoa(limitOrDefault(limit))}
	if state != "" {
		args = append(args, "--state", state)
	}
	var prs []PR
	if err := runJSON(dir, &prs, args...); err != nil {
		return nil, err
	}
	return prs, nil
}

// ViewPR fetches one pull request by number, with its body and comments.
func ViewPR(dir string, number int) (*PR, error) {
	var pr PR
	if err := runJSON(dir, &pr, "pr", "view", strconv.Itoa(number), "--json", prViewFields); err != nil {
		return nil, err
	}
	return &pr, nil
}

// CurrentPR looks up the pull request open from the branch checked out in
// dir, if there is one -- what the CI panel shows without anybody having to
// know their PR's number. It answers (nil, nil), not an error, when the
// branch has no PR: gh's own "no pull requests found" is not a failure here,
// just an empty answer.
func CurrentPR(dir string) (*PR, error) {
	var pr PR
	err := runJSON(dir, &pr, "pr", "view", "--json", prViewFields)
	if err != nil {
		if isNoneFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &pr, nil
}

// PRCreateOptions is what CreatePR needs. Title and Body are required; Base
// is the branch to merge into, and empty means the repository's own default
// branch, gh's choice to make. Draft opens it as a draft.
type PRCreateOptions struct {
	Title string
	Body  string
	Base  string
	Draft bool
}

// CreatePR opens a pull request from the branch checked out in dir. dir's
// branch has to already be pushed with an upstream -- the same thing Push in
// internal/gitx sets up -- or gh refuses with its own explanation of that.
func CreatePR(dir string, opts PRCreateOptions) (*PR, error) {
	args := []string{"pr", "create", "--title", opts.Title, "--body-file", "-"}
	if opts.Base != "" {
		args = append(args, "--base", opts.Base)
	}
	if opts.Draft {
		args = append(args, "--draft")
	}
	url, err := runWithStdin(dir, opts.Body, args...)
	if err != nil {
		return nil, err
	}
	return ViewPR(dir, prNumberFromURL(url))
}

// CommentPR posts a comment on a pull request.
func CommentPR(dir string, number int, body string) (*Comment, error) {
	url, err := runWithStdin(dir, body, "pr", "comment", strconv.Itoa(number), "--body-file", "-")
	if err != nil {
		return nil, err
	}
	_ = url // gh prints the comment's URL; the caller re-lists to show it in context.
	return &Comment{Body: body}, nil
}

// limitOrDefault keeps a listing bounded even when the caller passes 0.
func limitOrDefault(n int) int {
	if n <= 0 {
		return 30
	}
	return n
}
