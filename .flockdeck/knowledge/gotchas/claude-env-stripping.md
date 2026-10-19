---
type: Gotcha
title: Claude session variables are stripped from panes
description: Variables a running Claude Code session injects are removed so a pane does not think it is a nested child.
resource: internal/agent/builtin.go
tags: [claude, environment]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/builtin.go]
---
`ClaudeStripEnv()` returns `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_SSE_PORT` and `CLAUDE_CODE_DONT_INHERIT_ENV`. If a pane inherited them it would act as a nested child session and, among other effects, stop saving its transcript.

The list is the built-in `claude` spec's `stripEnv`, and `session.EnvStripping` removes it whatever an `agents.json` entry for `claude` says. Merge clones the built-in's `StripEnv` and `Env` before decoding a user entry so a rejected entry cannot alter them. See [agent spec](/data/agent-spec.md).
