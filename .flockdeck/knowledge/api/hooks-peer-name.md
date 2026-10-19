---
type: HTTP Endpoint
title: POST /peer-name
description: A pane reports the name other Claude sessions use to address it (flockdeck peer-name).
resource: internal/hooks/hooks.go
tags: [hooks, peer-name, team]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
# POST /peer-name

Client: `hooks.PeerName(api, token, pane, name)`, 5 second timeout.

## Request

JSON `{"pane", "name", "token"}`, at most 1 MiB. `name` must not be blank (400).

## Response

204 on success. 400 with the handler's reason (for example an unknown pane), 403 bad token, 405 wrong method, 503 no handler.

## Why it exists

The name is assigned by infrastructure outside Flockdeck and known only to the agent, usually by calling its own ListAgents tool. The stored name becomes `Member.Peer` in the [team](/packages/team.md) roster and is shown through `AgentLabel`, which rewrites names that could pass for the user or for Flockdeck ([sender labels](/security/team-sender-labels.md)).
