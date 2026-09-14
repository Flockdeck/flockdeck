package ghcli

import (
	"strconv"
	"strings"
	"time"
)

// WorkflowRun is one Actions run, as `gh run list` reports it.
type WorkflowRun struct {
	DatabaseID   int64  `json:"databaseId"`
	WorkflowName string `json:"workflowName"`
	DisplayTitle string `json:"displayTitle"`
	// Status is "queued", "in_progress" or "completed".
	Status string `json:"status"`
	// Conclusion is set once Status is "completed": "success", "failure",
	// "cancelled", "skipped", "neutral", "timed_out", "action_required" or
	// "startup_failure".
	Conclusion string    `json:"conclusion"`
	Event      string    `json:"event"`
	HeadBranch string    `json:"headBranch"`
	URL        string    `json:"url"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

const runListFields = "databaseId,workflowName,displayTitle,status,conclusion,event,headBranch,url,createdAt,updatedAt"

// ListRuns lists Actions runs for branch, most recent first -- the workflow
// history the CI panel shows for whatever branch is checked out in dir.
func ListRuns(dir, branch string, limit int) ([]WorkflowRun, error) {
	args := []string{"run", "list", "--json", runListFields, "--limit", strconv.Itoa(limitOrDefault(limit))}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	var runs []WorkflowRun
	if err := runJSON(dir, &runs, args...); err != nil {
		return nil, err
	}
	return runs, nil
}

// Check is one status check on a pull request, as `gh pr checks` reports it
// -- a workflow job, or a check some other integration posted.
type Check struct {
	Name string `json:"name"`
	// State is the raw state gh reports: e.g. "SUCCESS", "FAILURE",
	// "IN_PROGRESS", "PENDING", "SKIPPED", "CANCELLED". Bucket below is the
	// simpler question most callers actually want answered.
	State string `json:"state"`
	// Bucket groups State into "pass", "fail", "pending", "skipping" or
	// "cancel" -- what Summarize is built from.
	Bucket   string `json:"bucket"`
	Link     string `json:"link"`
	Workflow string `json:"workflow"`
}

const checksFields = "name,state,bucket,link,workflow"

// PRChecks lists the status checks on a pull request -- the Actions jobs and
// any other integration's checks together, the same list the Checks tab of a
// PR on github.com shows.
func PRChecks(dir string, number int) ([]Check, error) {
	var checks []Check
	err := runJSON(dir, &checks, "pr", "checks", strconv.Itoa(number), "--json", checksFields)
	if err != nil {
		// A PR with no checks configured at all -- gh says "no checks
		// reported on the 'branch' branch" -- is not a failure, just nothing
		// to show.
		if isNoneFound(err) || strings.Contains(strings.ToLower(err.Error()), "no checks reported") {
			return nil, nil
		}
		return nil, err
	}
	return checks, nil
}

// Summary is the CI panel's headline for a set of checks: how many are still
// running, how many passed, how many failed, and Overall, the one word a
// pane header or a PR row can show.
type Summary struct {
	Pending int `json:"pending"`
	Passing int `json:"passing"`
	Failing int `json:"failing"`
	// Overall is "pending" while anything is still running, else "failing"
	// if anything failed, else "passing" if there was anything to check at
	// all, else "none".
	Overall string `json:"overall"`
}

// Summarize reduces a list of checks to one headline. It is kept apart from
// PRChecks so it can be tested against fixed input without running gh.
func Summarize(checks []Check) Summary {
	var s Summary
	for _, c := range checks {
		switch c.Bucket {
		case "pass":
			s.Passing++
		case "fail", "cancel":
			s.Failing++
		case "pending", "":
			s.Pending++
			// "skipping" counts toward neither pass nor fail nor pending: a
			// skipped check is not evidence of anything either way.
		}
	}
	switch {
	case s.Pending > 0:
		s.Overall = "pending"
	case s.Failing > 0:
		s.Overall = "failing"
	case s.Passing > 0:
		s.Overall = "passing"
	default:
		s.Overall = "none"
	}
	return s
}
