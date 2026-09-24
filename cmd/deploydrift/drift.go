package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// For these repos, merging to main deploys nothing on its own: a v* tag does.
const org = "Flockdeck"

const (
	// detailCap bounds the per-commit file lookups for one repo. Commits past
	// it are counted as shipping (the safe direction: a missed drift is the
	// failure this tool exists to prevent, an extra one is only noise).
	detailCap = 100
	// commitCap bounds how many commits since the tag are listed at all.
	commitCap = 300
)

// profile picks which paths count as "not shipped" for a repo.
type profile int

const (
	// serviceProfile is a Go service or module: its markdown is notes about
	// the code, not part of what is built.
	serviceProfile profile = iota
	// contentProfile is site and docs: their pages are the product, so a
	// markdown change there ships and must not be ignored.
	contentProfile
)

type repoSpec struct {
	Name     string
	Profile  profile
	InfoOnly bool // reported, never flagged, never opens the issue
}

var repos = []repoSpec{
	{Name: "flockdeck-relay", Profile: serviceProfile},
	{Name: "flockdeck-billing", Profile: serviceProfile},
	{Name: "flockdeck-site", Profile: contentProfile},
	{Name: "flockdeck-docs", Profile: contentProfile},
	{Name: "flockdeck-remote", Profile: serviceProfile},
	{Name: "flockdeck", Profile: serviceProfile, InfoOnly: true},
}

const (
	relayRepo  = "flockdeck-relay"
	remoteRepo = "flockdeck-remote"
)

// ignoredPath reports whether a change to path cannot change what is built or
// deployed. What is ignored, and why:
//   - .github/: CI and repo configuration; it is never in an image or a module.
//   - repository configuration that no build reads into an image or module:
//     .gitattributes, .editorconfig, .gitignore, .dockerignore and CODEOWNERS,
//     wherever they sit. A line-ending renormalisation is such a commit.
//   - test files (_test.go, *.test.*, *.spec.*, testdata/, tests/, __tests__/):
//     they are compiled or run only by CI.
//   - for service repos only, markdown at the repo root or under docs/: notes
//     about the code. Site and docs are excluded from this rule because their
//     pages are the product; there only the root README.md is a note.
//
// Everything else (go.mod, Dockerfile, deploy/, config, non-markdown under
// docs/) is treated as shipping, including anything unrecognised.
func ignoredPath(p profile, path string) bool {
	if strings.HasPrefix(path, ".github/") {
		return true
	}
	if isRepoConfigFile(path) {
		return true
	}
	if isTestFile(path) {
		return true
	}
	switch p {
	case serviceProfile:
		if strings.HasSuffix(path, ".md") && (!strings.Contains(path, "/") || strings.HasPrefix(path, "docs/")) {
			return true
		}
	case contentProfile:
		if path == "README.md" {
			return true
		}
	}
	return false
}

func isRepoConfigFile(path string) bool {
	switch path[strings.LastIndex(path, "/")+1:] {
	case ".gitattributes", ".editorconfig", ".gitignore", ".dockerignore", "CODEOWNERS":
		return true
	}
	return false
}

func isTestFile(path string) bool {
	segs := strings.Split(path, "/")
	base := segs[len(segs)-1]
	if strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	for _, s := range segs[:len(segs)-1] {
		switch s {
		case "testdata", "tests", "__tests__":
			return true
		}
	}
	return false
}

// ships is true unless every touched path (and, for a rename, the path it
// came from) is ignorable. A commit that touches nothing ships nothing.
func ships(p profile, files []commitFile) bool {
	for _, f := range files {
		if !ignoredPath(p, f.Filename) {
			return true
		}
		if f.PreviousFilename != "" && !ignoredPath(p, f.PreviousFilename) {
			return true
		}
	}
	return false
}

type semver [3]int

var releaseTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

func parseSemver(s string) (semver, bool) {
	m := releaseTag.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

func (a semver) less(b semver) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// API response shapes: only the fields read.
type commitFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
}

type commitInfo struct {
	SHA    string `json:"sha"`
	Commit struct {
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
	Files []commitFile `json:"files"`
}

type commitRef struct {
	SHA  string
	When time.Time
}

// repoReport is one table row.
type repoReport struct {
	Spec           repoSpec
	LatestTag      string
	Total          int // commits on main since the tag
	Shipping       int // of those, how many change something that ships
	OldestShipping *commitRef
	Approximate    bool // some commits were counted as shipping without a lookup
	Age            time.Duration
	Flagged        bool
}

// pinReport is the relay's embed gap against remote's latest tag.
type pinReport struct {
	Pin        string // as written in go.mod
	Latest     string // remote's latest tag
	LatestDate time.Time
	Behind     bool
	NonRelease bool // pinned to a pseudo-version or the like
	Age        time.Duration
	Flagged    bool
}

type picture struct {
	Threshold time.Duration
	Now       time.Time
	Repos     []repoReport
	Pin       *pinReport
}

// drifting is true when anything is flagged. Info-only repos never are.
func (p *picture) drifting() bool {
	for _, r := range p.Repos {
		if r.Flagged {
			return true
		}
	}
	return p.Pin != nil && p.Pin.Flagged
}

func latestTag(ctx context.Context, c *ghClient, repo string) (string, error) {
	var best string
	var bestV semver
	for page := 1; page <= 20; page++ {
		var refs []struct {
			Ref string `json:"ref"`
		}
		path := fmt.Sprintf("/repos/%s/%s/git/matching-refs/tags/v?per_page=100&page=%d", org, esc(repo), page)
		if err := c.getJSON(ctx, path, &refs); err != nil {
			return "", err
		}
		for _, r := range refs {
			tag := strings.TrimPrefix(r.Ref, "refs/tags/")
			if v, ok := parseSemver(tag); ok && (best == "" || bestV.less(v)) {
				best, bestV = tag, v
			}
		}
		if len(refs) < 100 {
			break
		}
	}
	if best == "" {
		return "", fmt.Errorf("%s/%s has no vMAJOR.MINOR.PATCH tag", org, repo)
	}
	return best, nil
}

func commitDate(ctx context.Context, c *ghClient, repo, ref string) (time.Time, error) {
	var ci commitInfo
	if err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s", org, esc(repo), esc(ref)), &ci); err != nil {
		return time.Time{}, err
	}
	return ci.Commit.Committer.Date, nil
}

// commitsSince lists commits on main after the tag, oldest first, and how many
// there are in all (which may exceed the list when it was capped).
func commitsSince(ctx context.Context, c *ghClient, repo, tag string) ([]commitInfo, int, error) {
	var all []commitInfo
	total := 0
	for page := 1; len(all) < commitCap; page++ {
		var cmp struct {
			TotalCommits int          `json:"total_commits"`
			Commits      []commitInfo `json:"commits"`
		}
		path := fmt.Sprintf("/repos/%s/%s/compare/%s...main?per_page=100&page=%d", org, esc(repo), esc(tag), page)
		if err := c.getJSON(ctx, path, &cmp); err != nil {
			return nil, 0, err
		}
		total = cmp.TotalCommits
		all = append(all, cmp.Commits...)
		if len(cmp.Commits) < 100 || len(all) >= total {
			break
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Commit.Committer.Date.Before(all[j].Commit.Committer.Date) })
	return all, total, nil
}

func analyseRepo(ctx context.Context, c *ghClient, spec repoSpec, now time.Time, threshold time.Duration) (repoReport, error) {
	rep := repoReport{Spec: spec}
	tag, err := latestTag(ctx, c, spec.Name)
	if err != nil {
		return rep, err
	}
	rep.LatestTag = tag
	commits, total, err := commitsSince(ctx, c, spec.Name, tag)
	if err != nil {
		return rep, err
	}
	rep.Total = total
	for i, ci := range commits {
		// Past the cap, or where the API cut the file list short (it stops at
		// 300 files), a commit cannot be judged: count it as shipping.
		known := i < detailCap
		var files []commitFile
		if known {
			var full commitInfo
			if err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s", org, esc(spec.Name), esc(ci.SHA)), &full); err != nil {
				return rep, err
			}
			files = full.Files
			if len(files) >= 300 {
				known = false
			}
		}
		if known && !ships(spec.Profile, files) {
			continue
		}
		if !known {
			rep.Approximate = true
		}
		rep.Shipping++
		if rep.OldestShipping == nil {
			rep.OldestShipping = &commitRef{SHA: ci.SHA, When: ci.Commit.Committer.Date}
		}
	}
	// Commits beyond the list (capped) are unjudged too.
	if total > len(commits) {
		rep.Shipping += total - len(commits)
		rep.Approximate = true
	}
	if rep.OldestShipping != nil {
		rep.Age = now.Sub(rep.OldestShipping.When)
		rep.Flagged = !spec.InfoOnly && rep.Age > threshold
	}
	return rep, nil
}

var remotePin = regexp.MustCompile(`(?im)^\s*(?:require\s+)?github\.com/Flockdeck/flockdeck-remote\s+(\S+)`)

func analysePin(ctx context.Context, c *ghClient, remoteLatest string, now time.Time, threshold time.Duration) (*pinReport, error) {
	raw, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/contents/go.mod?ref=main", org, relayRepo), "application/vnd.github.raw", nil, nil)
	if err != nil {
		return nil, err
	}
	m := remotePin.FindSubmatch(raw)
	if m == nil {
		return nil, errors.New("relay go.mod does not require github.com/Flockdeck/flockdeck-remote")
	}
	pr := &pinReport{Pin: string(m[1]), Latest: remoteLatest}
	pinV, pinOK := parseSemver(pr.Pin)
	latestV, _ := parseSemver(remoteLatest)
	switch {
	case !pinOK:
		pr.NonRelease, pr.Behind = true, true
	case pinV.less(latestV):
		pr.Behind = true
	}
	if pr.Behind {
		if pr.LatestDate, err = commitDate(ctx, c, remoteRepo, remoteLatest); err != nil {
			return nil, err
		}
		pr.Age = now.Sub(pr.LatestDate)
		pr.Flagged = pr.Age > threshold
	}
	return pr, nil
}

// analyse builds the whole picture. Any failure aborts it: a partial picture
// must never be allowed to close the tracking issue.
func analyse(ctx context.Context, c *ghClient, now time.Time, threshold time.Duration) (*picture, error) {
	pic := &picture{Threshold: threshold, Now: now}
	for _, spec := range repos {
		rep, err := analyseRepo(ctx, c, spec, now, threshold)
		if err != nil {
			return nil, describe(spec.Name, err)
		}
		pic.Repos = append(pic.Repos, rep)
	}
	var remoteLatest string
	for _, r := range pic.Repos {
		if r.Spec.Name == remoteRepo {
			remoteLatest = r.LatestTag
		}
	}
	pin, err := analysePin(ctx, c, remoteLatest, now, threshold)
	if err != nil {
		return nil, describe(relayRepo+" go.mod", err)
	}
	pic.Pin = pin
	return pic, nil
}

func describe(what string, err error) error {
	var ae *apiError
	if errors.As(err, &ae) && (ae.Status == 404 || ae.Status == 403 || ae.Status == 401) {
		return fmt.Errorf("reading %s: %w (DEPLOY_DRIFT_TOKEN cannot read this repo: it needs Contents: read on every repo in the list, and the token owner must have access to the private ones)", what, err)
	}
	return fmt.Errorf("reading %s: %w", what, err)
}
