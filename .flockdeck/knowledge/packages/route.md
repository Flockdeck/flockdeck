---
type: Package
title: route
description: Pure model-routing engine that picks a model tier for a piece of work from rules in agents.json.
resource: internal/route
tags: [routing, models, cost]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/route/route.go, internal/route/files.go, internal/route/log.go]
---
# internal/route

Decides which model a spawn or a turn runs on. Deciding reads no file and makes no request; the caller records the result on the pane.

## API

- `New(policy) *Router`, `Router.Decide(Input) Decision`, `Decide(policy, input)`, `Router.On()`.
- `Input{Kind, Task, Agent, Current, NoDowngrade, OtherAgent}`; `Kind` is `spawn` or `turn`.
- `Decision{Model, Tier, Routed, Up, Agent, Source, Rule, Reason}`; `Source` is `rule` or `fallback`.
- `BuiltinRules()`, `RulesOf(policy)`, `MatchGlob`, `TierOf`.
- Log: `LogEntry`, `ReadLog`, `Stats`, `StatsByOverrideRate`, `ClearLog` ([routing log](/data/routing-log.md)).

## Behaviour

- Policy settings: [mode](/config/routing-mode.md), [floor](/config/routing-floor.md), [strategy](/config/routing-strategy.md), [crossAgent](/config/routing-cross-agent.md). Format: [routing policy](/data/routing-policy.md).
- First matching rule wins. No match leaves the work alone unless strategy is `cost`, which moves it to the cheapest tier at or above the floor.
- The floor raises a rule's tier (the reason notes it). `NoDowngrade` blocks moves to a smaller tier. A model with no tier is never routed from or to.
- Within a tier, `pick` keeps the current model if it is in that tier, otherwise the cheapest by `pricing.Lookup` (input plus output price), or the first listed if any lacks a price.
- Cross-agent moves need all of: policy `crossAgent`, `Kind == spawn`, and an `OtherAgent` resolver that says the agent is ready.
- Built-in rules (used when a policy has `rules` absent; an empty list means none): "hard work" (top; task regex for refactor, redesign, architect, concurren, race, deadlock, migration, security), "schema changes" (top; files `**/migrations/**`, `**/*.sql`), "run the tests" (small), "rename or move" (small, at most 25 words), "changelog and docs wording" (small, at most 40 words).
- File matching: paths are pulled from task text by regexp; globs use `path.Match` per part with `**`, case-insensitive; a glob without a slash matches the last path part.

Imports [agent](/packages/agent.md) and `pricing`.
