---
type: Gotcha
title: Routing rule order and non-matches
description: First match wins, so rule order matters, and some rules deliberately do not match to let later rules run.
resource: internal/route/route.go
tags: [routing, rules]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/route/route.go]
---
- The first rule that matches decides, even when it then changes nothing ("matched, but the work is already on X"). The built-in rules put hard work before cheap work, so "move the session store behind a lock to fix the race" is routed up, not down.
- A rule naming another agent's model does not match work on this agent unless cross-agent routing is allowed. Counting it as a match that changed nothing once stopped every later rule from being tried.
- A model with no known tier (Claude Code's Default, `opusplan`) is never routed from or to. A rule that matches it reports why in `Decision.Reason`.
- The `fanout` kind no longer exists; a rule with it is skipped with a notice. A saved `suggest` mode is read as off.
- A glob without a slash matches the last path part only, and globs are lower-cased with backslashes turned to slashes, so `db\migrations\**` and `./migrations/**` work.
- Rules in an absent `rules` key are the built-in ones; an explicit empty list means no rules.

See [routing policy](/data/routing-policy.md).
