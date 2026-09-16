package ghcli

import "strconv"

// ListIssues lists issues in the repository dir sits in. state is one of
// "open", "closed" or "all"; "" means "open", gh's own default.
func ListIssues(dir, state string, limit int) ([]Issue, error) {
	args := []string{"issue", "list", "--json", listFields, "--limit", strconv.Itoa(limitOrDefault(limit))}
	if state != "" {
		args = append(args, "--state", state)
	}
	var issues []Issue
	if err := runJSON(dir, &issues, args...); err != nil {
		return nil, err
	}
	return issues, nil
}

// ViewIssue fetches one issue by number, with its body and comments.
func ViewIssue(dir string, number int) (*Issue, error) {
	var is Issue
	if err := runJSON(dir, &is, "issue", "view", strconv.Itoa(number), "--json", viewFields); err != nil {
		return nil, err
	}
	return &is, nil
}

// IssueCreateOptions is what CreateIssue needs.
type IssueCreateOptions struct {
	Title string
	Body  string
}

// CreateIssue opens an issue in the repository dir sits in.
func CreateIssue(dir string, opts IssueCreateOptions) (*Issue, error) {
	url, err := runWithStdin(dir, opts.Body, "issue", "create", "--title", opts.Title, "--body-file", "-")
	if err != nil {
		return nil, err
	}
	return ViewIssue(dir, prNumberFromURL(url))
}

// CommentIssue posts a comment on an issue.
func CommentIssue(dir string, number int, body string) (*Comment, error) {
	if _, err := runWithStdin(dir, body, "issue", "comment", strconv.Itoa(number), "--body-file", "-"); err != nil {
		return nil, err
	}
	return &Comment{Body: body}, nil
}
