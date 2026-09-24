package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	issueTitle = "Deploy drift: merged but not tagged"
	// trackerMarker identifies the issue as this tool's. Together with the
	// author check below it stops the tool editing an issue somebody else
	// opened with the same title on this public repo.
	trackerMarker = "<!-- deploy-drift:tracker -->"
	// botLogin is who GITHUB_TOKEN acts as; only its issues are ours.
	botLogin = "github-actions[bot]"
)

var fingerprintRe = regexp.MustCompile(`<!-- deploy-drift:fingerprint:([0-9a-f]+|clear) -->`)

// fingerprint is what a re-run must not change to stay quiet. It covers who is
// flagged and what they are flagged for, and deliberately leaves out ages and
// unflagged rows, which move every day without the picture changing.
func fingerprint(p *picture) string {
	var lines []string
	for _, r := range p.Repos {
		if r.Flagged {
			lines = append(lines, fmt.Sprintf("%s|%s|%d|%s", r.Spec.Name, r.LatestTag, r.Shipping, r.OldestShipping.SHA))
		}
	}
	if p.Pin != nil && p.Pin.Flagged {
		lines = append(lines, fmt.Sprintf("pin|%s|%s", p.Pin.Pin, p.Pin.Latest))
	}
	if len(lines) == 0 {
		return "clear"
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

func formatAge(d time.Duration) string {
	if d < time.Hour {
		return "<1h"
	}
	h := int(d.Hours())
	if h < 24 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd %dh", h/24, h%24)
}

func formatThreshold(d time.Duration) string {
	h := d.Hours()
	if h == float64(int(h)) {
		return fmt.Sprintf("%dh", int(h))
	}
	return fmt.Sprintf("%.1fh", h)
}

// Only values this program built or validated reach the output: tags matched
// the release pattern, SHAs are hex from the API, the pin was matched as one
// non-space token. No commit message or other API free text is rendered.
func pinCell(p *picture, name string) string {
	if name != relayRepo || p.Pin == nil {
		return "n/a"
	}
	pin := p.Pin
	switch {
	case !pin.Behind:
		return fmt.Sprintf("in step (`%s`)", pin.Pin)
	case pin.NonRelease:
		return fmt.Sprintf("pinned to `%s`, not a release; remote's latest is `%s` (%s old)", pin.Pin, pin.Latest, formatAge(pin.Age))
	default:
		return fmt.Sprintf("pins `%s`, remote's latest is `%s` (%s old)", pin.Pin, pin.Latest, formatAge(pin.Age))
	}
}

func statusCell(r repoReport) string {
	switch {
	case r.Spec.InfoOnly && r.Shipping > 0:
		return "info only"
	case r.Spec.InfoOnly:
		return "info only, clean"
	case r.Flagged:
		return "**DRIFT**"
	case r.Shipping > 0:
		return "under threshold"
	case r.Total > 0:
		return "clean (only non-shipping commits)"
	}
	return "clean"
}

// renderBody is the issue body, identical in shape whether or not anything is
// flagged (a closed issue keeps the last picture).
func renderBody(p *picture) string {
	var b strings.Builder
	b.WriteString(trackerMarker + "\n")
	fmt.Fprintf(&b, "<!-- deploy-drift:fingerprint:%s -->\n", fingerprint(p))
	if p.drifting() {
		b.WriteString("Merging to `main` deploys nothing for these repos. A `v*` tag builds the image, Flux pins it, the cluster rolls it out. The rows below have shipping changes on `main` that no tag has picked up yet.\n\n")
	} else {
		b.WriteString("Nothing is drifting: every repo is tagged up to its last shipping commit (or is within the threshold).\n\n")
	}
	b.WriteString("| Repo | Latest tag | Commits since | Shipping | Oldest shipping | Remote pin gap | Status |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range p.Repos {
		oldest := "n/a"
		if r.OldestShipping != nil {
			oldest = fmt.Sprintf("%s (`%s`)", formatAge(r.Age), shortSHA(r.OldestShipping.SHA))
		}
		ship := fmt.Sprint(r.Shipping)
		if r.Approximate {
			ship = "≥" + ship
		}
		fmt.Fprintf(&b, "| %s/%s | `%s` | %d | %s | %s | %s | %s |\n", org, r.Spec.Name, r.LatestTag, r.Total, ship, oldest, pinCell(p, r.Spec.Name), statusCell(r))
	}
	fmt.Fprintf(&b, "\nFlagged once the oldest untagged *shipping* commit is older than **%s**. Last checked %s.\n", formatThreshold(p.Threshold), p.Now.UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("\n**What counts as shipping.** A commit is ignored when it touches only `.github/`, test files (`_test.go`, `*.test.*`, `*.spec.*`, `testdata/`, `tests/`, `__tests__/`), or, for the Go repos, markdown at the root or under `docs/`. For `flockdeck-site` and `flockdeck-docs` markdown is the product, so only the root `README.md` is ignored. A `≥` means some commits could not be inspected and were counted as shipping.\n")
	b.WriteString("\n**To clear a row:** tag the repo. For a `flockdeck-remote` change to reach users: tag remote, bump the `flockdeck-remote` line in relay's `go.mod`, then tag relay.\n")
	b.WriteString("\n_Maintained by `.github/workflows/deploy-drift.yml`. This issue is edited in place and closes itself when nothing is drifting._\n")
	return b.String()
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// changeComment says, briefly, what a changed picture is now. Built from the
// same validated values as the body.
func changeComment(p *picture) string {
	var b strings.Builder
	b.WriteString("The drift picture changed:\n\n")
	for _, r := range p.Repos {
		if r.Flagged {
			fmt.Fprintf(&b, "- `%s`: %d shipping commit(s) since `%s`, oldest %s old\n", r.Spec.Name, r.Shipping, r.LatestTag, formatAge(r.Age))
		}
	}
	if p.Pin != nil && p.Pin.Flagged {
		fmt.Fprintf(&b, "- relay embeds `flockdeck-remote` `%s`; remote's latest is `%s` (%s old)\n", p.Pin.Pin, p.Pin.Latest, formatAge(p.Pin.Age))
	}
	return b.String()
}

type issue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	State       string `json:"state"`
	PullRequest *struct {
	} `json:"pull_request"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (i issue) ours() bool {
	return i.PullRequest == nil && i.Title == issueTitle && i.User.Login == botLogin && strings.Contains(i.Body, trackerMarker)
}

// findTracker returns the newest of this tool's issues in the given state.
func findTracker(ctx context.Context, c *ghClient, self, state string) (*issue, error) {
	for page := 1; page <= 10; page++ {
		var list []issue
		path := fmt.Sprintf("/repos/%s/issues?state=%s&sort=created&direction=desc&per_page=100&page=%d", self, state, page)
		if err := c.getJSON(ctx, path, &list); err != nil {
			return nil, err
		}
		for _, i := range list {
			if i.ours() {
				return &i, nil
			}
		}
		if len(list) < 100 {
			break
		}
	}
	return nil, nil
}

type syncResult struct {
	Action    string // created, reopened, updated, closed, unchanged, none
	Commented bool
}

// syncIssue makes the tracking issue match the picture. It comments only when
// the fingerprint changed, and edits the body in place otherwise, so a re-run
// with the same picture is silent.
func syncIssue(ctx context.Context, c *ghClient, self string, p *picture) (syncResult, error) {
	body := renderBody(p)
	fp := fingerprint(p)
	open, err := findTracker(ctx, c, self, "open")
	if err != nil {
		return syncResult{}, err
	}
	base := fmt.Sprintf("/repos/%s/issues", self)

	if fp == "clear" {
		if open == nil {
			return syncResult{Action: "none"}, nil
		}
		if _, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/comments", base, open.Number), "", map[string]string{"body": "Nothing is drifting any more; closing."}, nil); err != nil {
			return syncResult{}, err
		}
		_, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", base, open.Number), "", map[string]string{"body": body, "state": "closed", "state_reason": "completed"}, nil)
		return syncResult{Action: "closed", Commented: true}, err
	}

	if open != nil {
		res := syncResult{Action: "unchanged"}
		if open.Body != body {
			if _, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", base, open.Number), "", map[string]string{"body": body}, nil); err != nil {
				return syncResult{}, err
			}
			res.Action = "updated"
		}
		if m := fingerprintRe.FindStringSubmatch(open.Body); m == nil || m[1] != fp {
			if _, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/comments", base, open.Number), "", map[string]string{"body": changeComment(p)}, nil); err != nil {
				return syncResult{}, err
			}
			res.Commented = true
		}
		return res, nil
	}

	// Drift with no open tracker: bring the last one back rather than start a
	// new issue each time the picture crosses the threshold.
	closed, err := findTracker(ctx, c, self, "closed")
	if err != nil {
		return syncResult{}, err
	}
	if closed != nil {
		if _, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", base, closed.Number), "", map[string]string{"body": body, "state": "open"}, nil); err != nil {
			return syncResult{}, err
		}
		_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/comments", base, closed.Number), "", map[string]string{"body": changeComment(p)}, nil)
		return syncResult{Action: "reopened", Commented: true}, err
	}
	_, err = c.do(ctx, http.MethodPost, base, "", map[string]string{"title": issueTitle, "body": body}, nil)
	return syncResult{Action: "created"}, err
}
