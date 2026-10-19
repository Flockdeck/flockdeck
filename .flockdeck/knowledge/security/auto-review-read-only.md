---
type: Security Boundary
title: Auto-review of read-only Bash calls
description: How internal/review decides a PreToolUse Bash call is safe to allow without a permission prompt.
resource: internal/review
tags: [security, permissions, bash, git]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/review]
---
# Auto-review

`review.Decide` is one-sided: it returns allow or "ask", never deny. Wrongly allowing something dangerous is the real harm; wrongly asking only costs an interruption.

## What can be allowed

Only the `Bash` tool, with a command of at most 4 KiB (`maxCommandBytes`; the hook clips tool input at 64 KiB per field, so a longer command is never judged by its prefix). Everything else (Edit, Write, MultiEdit, MCP) falls through to the normal prompt.

A command is allowed only if all hold:

1. It contains none of `| & ; < > backtick $ ( ) { } newline ' " \ * ? [` (`metacharacters`).
2. No argument is outside the project (absolute path, leading `~`, drive letter, or containing `..`) or names a secret file (`.env`, `.env.*`, private `id_*` keys but not `.pub`, `.npmrc`, `.netrc`, `.pgpass`, `.git-credentials`, names containing `credential`, and `.pem .key .p12 .pfx .ppk .keystore .jks`). `--flag=value` is checked on the value.
3. The program is one of `ls pwd cat head tail wc grep which whoami diff stat cut basename dirname true type`, or a git subcommand in `status diff log show blame rev-parse describe`. Deliberately excluded: `env`, `sort -o`, `rg --pre`, `file -C`, `date -s`, `printenv`, and every `go` subcommand.
4. For git: no `--output`, `--ext-diff`, `--textconv` or `--submodule` flag, and `gitRunsNothing` agrees.

## `gitRunsNothing`

Runs `git config --null --get-regexp` for keys that make a read-only subcommand run a program or reach the network (`core.fsmonitor`, `diff.external`, diff drivers' `command`/`textconv`, filter `clean`/`smudge`/`process`, gpg programs, `diff.submodule`, `trace2.*`, `extensions.partialclone`, `remote.*.promisor`), within 5 s. A `core.fsmonitor` boolean and Git LFS's own filter count as harmless. For `status`, `diff` and `describe` it also refuses when the checkout has a submodule, because the submodule's own config is not visible from the top. Any failure, empty directory or timeout means "ask".

Wiring: `hooks.SetReviewHandler` calls the workspace's `reviewTool` only for panes with auto-review on; the reply carries `permissionDecision: allow` and a reason ([hook route](/api/hooks-hook.md)). `SecretPath` is reused by the transcript recorder.
