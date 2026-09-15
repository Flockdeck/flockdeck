package ghcli

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
