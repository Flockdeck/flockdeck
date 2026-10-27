---
type: Package
title: acp manifest
description: The adapters and exact versions a Flockdeck release is tested with for ACP agents, the Node check, and the canary handshake.
resource: internal/acp/manifest/manifest.go
tags: [acp, agents, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/acp/manifest/manifest.go, internal/acp/manifest/adapters.json, internal/acp/manifest/probe.go, internal/acp/manifest/handshake.go]
---
Direction (27 Oct 2026): Flockdeck is dropping Node and third-party ACP adapters: Go adapters in the flockdeck binary replace them (T204, T206), and the Node check becomes a generic "agent unavailable" state (T207). The Node check is gone (T207): an agent that cannot be found or started is `agent.UnavailableText`. The Go adapters are not done yet. See docs/acp-direction.md.

- `adapters.json` is embedded and read by `Default()` (a bad one panics at start, and the tests parse it). Each adapter has a command and args, an npm package (for the canary), either an exact `pin` or a `minVersion` floor, and `testedWith`/`testedOn`. An adapter may carry `launchEnv` (up to four capital-letter variables with fixed values, never Flockdeck's own prefixes); `structured.Runtime.env` adds it after `passEnv` and it wins. opencode uses it to set `OPENCODE_CONFIG_CONTENT` so `bash` and `edit` ask. `Parse` refuses unknown fields, duplicate ids, ranges, tags and `latest` as versions, and any adapter for Claude Code.
- `Adapter.Accepts(have)` is the pin exactly, or the floor and up. `ParseVersion` takes a bare version or the output of `--version`; `Compare` orders pre-releases below their release.
- `Probe` (`LookPath`) and `SystemProbe` are how the runtime looks for the adapter's command. There is no Node check and no install text.
- `Handshake` starts an adapter by absolute path with exactly the environment it is given, sends `initialize` (protocol 1, no client capabilities), reads the answer past noise, and ends the adapter and what it started with `internal/proctree`, without waiting for stdout or stderr to close (a descendant can hold them). It never logs in or opens a session.
- `Manifest.Table()` renders the "tested with" table; `docs/acp-adapters.md` holds it between markers and a test keeps them equal. See [acp-canary](../ci/acp-canary.md) for the weekly job.
