---
type: Package
title: ghcli
description: GitHub access by running the gh command-line tool: PRs, issues, CI checks, repo info, login and install.
resource: internal/ghcli/ghcli.go
tags: [github, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/ghcli/ghcli.go, internal/ghcli/pr.go, internal/ghcli/issue.go, internal/ghcli/ci.go, internal/ghcli/models.go, internal/ghcli/install.go, internal/ghcli/auth.go, internal/ghcli/repo.go]
---
Runs the `gh` binary (found on PATH) and reads its `--json` output, the way [gitx](/packages/gitx.md) runs git. It stores no token; `gh` keeps its own login.

## API

| Area | Functions and types |
|---|---|
| Presence | `Installed`, `DetectInstaller`, `TryInstall`, `Platform` |
| Auth | `GetAuthStatus` (`gh auth status --hostname github.com`; "not logged in" is not an error), `Login` (streams `LoginEvent` {Code, Line, Done, Err}), `Logout` |
| Pull requests | `ListPRs`, `ViewPR`, `CurrentPR`, `CreatePR`, `CommentPR` |
| Issues | `ListIssues`, `ViewIssue`, `CreateIssue`, `CommentIssue` |
| CI | `ListRuns` (`WorkflowRun`), `PRChecks` (`Check`), `Summarize` (`Summary`) |
| Repo | `Info` (`RepoInfo`) |
| Models | `Actor`, `Comment`, `PR`, `Issue` |

## Behaviour

- Each call needs a working directory (an empty one is an error, except `GetAuthStatus` which falls back to the process cwd).
- Timeouts: 30 s per call; `Login` 10 min; install 5 min.
- Child environment adds `GH_PROMPT_DISABLED=1`, `GH_NO_UPDATE_NOTIFIER=1`, empty `GH_PAGER` and `PAGER`, so gh fails rather than prompts. `Login` clears `GH_PROMPT_DISABLED` and runs `gh auth login --hostname github.com --git-protocol https --web`, sending a newline on stdin to skip the "press Enter" step and parsing the one-time code from stderr.
- Bodies go on stdin (`--body-file -`) to avoid quoting and length limits. Errors name only `gh <subcommand>` and keep up to 6 lines of gh's message.
- `DetectInstaller` looks for winget, brew, apt-get, dnf, pacman, apk in that order and always offers the manual download URL.
- `sysproc.NoWindow` stops console windows flashing on Windows.
