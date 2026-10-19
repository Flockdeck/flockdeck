package ghcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RepoInfo is what CI and the PR/issue panels show about the repository a
// working tree belongs to.
type RepoInfo struct {
	NameWithOwner    string `json:"nameWithOwner"`
	URL              string `json:"url"`
	DefaultBranchRef struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
}

// Info asks gh which GitHub repository dir belongs to -- gh works this out
// from the checkout's remotes the same way `gh pr create` does, so a caller
// never has to parse a remote URL itself. It fails with gh's own explanation
// when dir is not inside a GitHub repository at all (no remote, or a remote
// pointed somewhere else).
func Info(dir string) (*RepoInfo, error) {
	var info RepoInfo
	if err := runJSON(dir, &info, "repo", "view", "--json", "nameWithOwner,url,defaultBranchRef"); err != nil {
		return nil, err
	}
	return &info, nil
}

// visibilityTimeout bounds Visibility. It is shorter than commandTimeout
// because it is asked in the background for a decision that has a safe default,
// and a slow answer is no better than none.
const visibilityTimeout = 10 * time.Second

// Visibility returns the lower-case visibility gh reports for the repository
// dir belongs to: "public", "private" or "internal". It fails when gh is not
// installed or signed in, when the machine is offline, when dir has no GitHub
// remote, or when the call takes longer than visibilityTimeout. A caller must
// treat every failure as "not known to be private".
func Visibility(dir string) (string, error) {
	text, err := runCtx(context.Background(), visibilityTimeout, dir, "repo", "view", "--json", "visibility")
	if err != nil {
		return "", err
	}
	return parseVisibility(text)
}

// parseVisibility reads the answer to `gh repo view --json visibility`.
func parseVisibility(text string) (string, error) {
	var v struct {
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return "", fmt.Errorf("gh repo view: could not read its answer: %w", err)
	}
	return strings.ToLower(strings.TrimSpace(v.Visibility)), nil
}
