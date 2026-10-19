---
type: Data Type
title: Agent spec
description: The Spec struct that describes one agent, with its runner, argument template, models, capabilities and environment.
resource: internal/agent/spec.go
tags: [agent, spec, catalog]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/spec.go, internal/agent/catalog.go, internal/agent/config.go]
---
# Spec

One entry of the catalog in [agent](/packages/agent.md). JSON names are the keys used in `agents.json` ([agents.json](/data/agents-json.md)).

| Field | JSON | Meaning |
|---|---|---|
| `ID`, `Name` | `id`, `name` | Id is matched exactly (case-sensitive). Name defaults to the id |
| `Runner` | `runner` | `cli` (external command in the pane's pseudo-terminal) or `api` (Flockdeck's own `flockdeck chat`) |
| `Exe` | `exe` | Program to look for on PATH, for `cli` |
| `Args`, `ResumeArgs` | `args`, `resumeArgs` | Argument template; resume replaces it when reattaching |
| `Models`, `DefaultModel` | `models`, `defaultModel` | `Model{id, name, note, tier}`; tier is `small`, `mid` or `top`; empty default leaves the choice to the agent |
| `Env`, `StripEnv` | `env`, `stripEnv` | Variables added to, and removed from, the pane environment. `$VAR` in an `env` value is expanded; the name is not |
| `API` | `api` | `{wire, baseURL, keyEnv}`; wire is `anthropic`, `openai` or `gemini` |
| `Caps` | `caps` | `hooks`, `resume`, `transcript`, `trust`, `context` (`hook`, `prompt`, `task` or `none`) |
| `Patterns` | `patterns` | `waiting` and `idle` regular expressions, used only when `caps.hooks` is false |
| `Install` | `install` | Text shown when the agent is not available |
| `NoDashDash` | `noDashDash` | Suppress the `--` that guards a prompt starting with `-` |
| `Hidden` | `hidden` | Keep out of the picker |

## Argument template

`Arg` is a literal `value`, or a group `{if: "<token>", args: [...]}` used only when the token has a value, so a flag is never left without its value. Tokens: `session`, `model`, `settings`, `prompt`, `cwd`, `pane`; written `{{name}}`, spaces inside the braces allowed.

## Context modes

`hook`: the briefing is returned in the reply to `SessionStart`. `prompt`: put in front of the opening prompt. `task`: same, but only when there is a task (for agents that exit after one message, such as Aider's `--message`). `none`.

Built-in model tiers, for example `claude`: `opus` top, `sonnet` mid, `haiku` small, `opusplan` untiered.
