---
type: WebSocket Message
title: Control commands: git, review, worktrees, GitHub
description: Commands on /ws/control for the review panel, git remotes, worktrees and the gh-based GitHub panel.
resource: internal/server/changes.go
tags: [websocket, control, git, github]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/changes.go, internal/server/control.go, internal/server/viewerrole.go, internal/server/ghcli.go, internal/server/worktrees.go, internal/server/gitprograms.go, internal/server/browse.go, internal/server/browse_windows.go]
---
Part of [/ws/control](/api/ws-control.md). Files: `changes.go`, `worktrees.go`, `ghcli.go`, `gitprograms.go`, `browse.go`. All `accessFull` (refused to viewers).

## Review panel (`changes.go`)

| `cmd` | Fields | Reply |
|---|---|---|
| `changes` | `path`, `follow` | `changes` message: cwd, branch, upstream, ahead, behind, hasRemote, files |
| `diff` | `path`, `text` (file) | `diff` message: cwd, file, text, error |
| `commit` | `path`, `text` (message), `push`, `files`, `stamps`, `omitted`, `gitAccept`, `gitSeen` | notice, then fresh `changes`. Refused if the tree no longer matches `stamps` |
| `gitPush`, `gitPull`, `gitFetch` | `path`, `gitAccept`, `gitSeen` | notice and `changes` |

`gitWarn` (server to window) is sent when git config would run a program from the repository; the window answers by resending with `gitAccept` of `once` or `remember` and `gitSeen` equal to the warned fingerprint. A relay window cannot accept. See `gitprograms.go` and [git programs file](/data/git-programs-file.md).

## Worktrees (`worktrees.go`)

| `cmd` | Fields | Effect |
|---|---|---|
| `worktrees` | `root` | `worktrees` message: root, defaultBase, items, branches, repos, error |
| `worktreeAdd` | `root`, `text` (branch), `base`, `path` | Create |
| `worktreeRemove` | `root`, `path`, `force` | Remove |
| `worktreePrune` | `root` | Prune |

## Folder browser (`browse.go`)

`browse` with `path` replies `browse`: path, parent, isRepo, entries, places, error. On Windows `browse_windows.go` lists drives.

## GitHub panel (`ghcli.go`, uses the `gh` CLI)

`ghStatus`, `ghInstall`, `ghLogin`, `ghLoginCancel`, `ghLogout`, `ghPRs` (`ghState`), `ghPR` (`ghNumber`), `ghPRCreate` (`ghTitle`, `ghBody`, `ghBase`, `ghDraft`), `ghPRComment` (`ghNumber`, `ghBody`), `ghIssues`, `ghIssue`, `ghIssueCreate`, `ghIssueComment`, `ghChecks`. Replies are `ghStatus`, `ghProgress`, `ghPRs`, `ghPR`, `ghIssues`, `ghIssue`, `ghChecks` messages.
