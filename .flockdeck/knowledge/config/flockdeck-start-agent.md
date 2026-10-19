---
type: Environment Variable
title: FLOCKDECK_START_AGENT
description: Default agent id new panes start as; the environment form of -agent.
resource: main.go
tags: [config]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
Seeds the default of [flockdeck](/cli/flockdeck.md) `-agent`; a flag wins. The value is checked against the catalog only for a run that starts an agent, so a stale value does not stop `flockdeck -quit`.

It has its own name because `FLOCKDECK_AGENT` is already read by each pane's [chat](/cli/chat.md) process to learn which agent it is. Reusing that name would give every pane's chat the service's default agent.
