---
type: Gotcha
title: Prompt token expansion and the -- guard
description: How BuildArgv expands tokens in one pass, drops value-less groups and guards prompts that start with a dash.
resource: internal/agent/spec.go
tags: [agent, argv, tokens]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/spec.go]
---
- **One pass.** `Tokens.Expand` uses a single regexp replace, so a value is never rescanned. Replacing tokens one name at a time rewrote `{{pane}}` inside an opening task that came earlier in the order.
- **Spaces in braces work.** `{{ model }}` is accepted. It used to reach the CLI as a literal model name.
- **Groups keep an option with its value.** `["--model", "{{model}}"]` with no model drops both, so a flag never swallows the next argument.
- **Unknown tokens are caught at load.** `checkTokens` reports an `if` or `{{...}}` that names none of `session`, `model`, `settings`, `prompt`, `cwd`, `pane`; otherwise a typo like `modle` silently dropped a flag.
- **`--` guard.** A lone `{{prompt}}` argument whose expansion starts with `-` is preceded by `--`, so the task `-p is not what I meant` is not read as an option and does not kill the pane. Not applied inside a group (it would become the option's value) or when `noDashDash` is set.
- **`task` context mode** launches a pane with no task bare, because a message made only of briefing would be answered and the agent would exit.
