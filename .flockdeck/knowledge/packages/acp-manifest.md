---
type: Package
title: acp manifest
description: The adapters and exact versions a Flockdeck release is tested with for ACP agents, the Node check, and the canary handshake.
resource: internal/acp/manifest/manifest.go
tags: [acp, agents, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/acp/manifest/manifest.go, internal/acp/manifest/adapters.json, internal/acp/manifest/node.go, internal/acp/manifest/handshake.go]
---
- `adapters.json` is embedded and read by `Default()` (a bad one panics at start, and the tests parse it). Each adapter has a command and args, an npm package, either an exact `pin` or a `minVersion` floor, `needsNode` with `minNode`, and `testedWith`/`testedOn`. `Parse` refuses unknown fields, duplicate ids, ranges, tags and `latest` as versions, and any adapter for Claude Code.
- `Adapter.Accepts(have)` is the pin exactly, or the floor and up. `ParseVersion` takes a bare version or the output of `--version`; `Compare` orders pre-releases below their release.
- `CheckNode(ctx, adapter, probe)` returns `*NodeRequiredError` (problem `missing`, `too old` or `unusable`, with the version found, the version needed and the fix text) for an adapter that needs Node. Flockdeck does not bundle or install Node. `SystemProbe` runs `node --version` by full path with only PATH in its environment.
- `Handshake` starts an adapter by absolute path with exactly the environment it is given, sends `initialize` (protocol 1, no client capabilities), reads the answer past noise, and ends the adapter and its process group. It never logs in or opens a session.
- `Manifest.Table()` renders the "tested with" table; `docs/acp-adapters.md` holds it between markers and a test keeps them equal. See [acp-canary](../ci/acp-canary.md) for the weekly job.
