---
type: File Format
title: agents.json
description: The user's own agents, roles, defaults and routing policy, merged over the built-ins and re-read on every catalog load.
resource: internal/agent/config.go
tags: [agent, config-file, routing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/config.go]
---
# agents.json

Located in the state (config) directory as `agents.json`. Read fresh each time the catalog is requested, so a hand edit applies without a restart.

## Top-level keys

| Key | Type | Meaning |
|---|---|---|
| `version` | int | `1` (`ConfigVersion`). Nothing rejects a file for its version |
| `defaults` | `{agent, model, routing}` | Installation-wide default agent and model |
| `projects` | map path to the same | Per-project defaults and routing policy; keys compared by `projectKey` so two spellings of one path are one project |
| `agents` | list of [agent spec](/data/agent-spec.md) | Merged field by field over a built-in with the same id; a new id adds an agent |
| `roles` | list of [roles](/data/agent-role.md) | Same merge rule |
| `routing` | [routing policy](/data/routing-policy.md) | Policy for every project without its own |

Any other top-level key is kept in `File.Extra` and written back, so saving a default from the picker cannot delete something a later build or the user put there. Entries stay as raw JSON so a field the user set can be told from one they omitted.

## Reading rules

- A missing, empty or whitespace-only file is "no file". A leading UTF-8 BOM is removed.
- A parse error gives `Catalog.Notice` plus the built-ins; the app still starts. A file that cannot be parsed is never written over (`SetDefaults`, `SetBaseURL` return the error).
- Problems in individual entries (no id, bad token name, bad tier, unknown field, unusable runner) are collected into one notice and cost only that entry. Unknown fields are reported except retired ones (`switch`).

## Writing rules

Writes are atomic (temp file then rename through `store.WriteAtomic`, mode 0600), serialised by a process-wide mutex, with HTML escaping off and two-space indent. `SetBaseURL` accepts only `http`/`https` addresses with a host and no user info, and puts the address on the last entry for the agent while clearing it from earlier entries. See [write rules](/gotchas/agents-json-write-rules.md).

Routing policy is never read from a file in a repository ([routing policy source](/security/routing-policy-source.md)).
