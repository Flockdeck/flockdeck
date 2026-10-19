---
type: Package
title: artifacts
description: Path-confinement core that turns a file on this machine into something a remote device may be shown read-only.
resource: internal/artifacts
tags: [security, remote, files]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/artifacts/doc.go, internal/artifacts/limits.go, internal/artifacts/names.go, internal/artifacts/registry.go, internal/artifacts/root.go, internal/artifacts/root_unix.go, internal/artifacts/root_windows.go, internal/artifacts/rootguard.go, internal/artifacts/rootpolicy.go, internal/artifacts/rootpolicy_home.go]
---
Opens files for the read-only remote artifacts viewer. A remote client never names a path: the host issues an opaque id (`Registry`) and re-opens the file with `Root.Open` on every request.

## Main types

| Name | Role |
|---|---|
| `Root` (`NewRoot`) | A project folder the host chose. `NewRoot` refuses empty or relative paths, filesystem roots, home directories, secret-keeping folders, system folders, and any root when no owned home can be found. |
| `Root.Open(candidate)` | Returns a `File` that is a regular, single-link file inside the root, or an error for which `errors.Is(err, ErrUnavailable)` holds. |
| `File.Limited(n)` | Reader that serves at most `MaxViewBytes` (5 MiB) in all. |
| `Reason` / `ReasonOf` / `CauseOf` | Why a path was refused (lexical, outside, link, denied, not-regular, hardlink, changed, missing, mount, alias, read). For this machine's log only; a client sees only "artifact unavailable". |
| `Registry` | Per-socket map of random ids (128 bits, base64url) to `Entry{Kind, Pane, Root, Path}`. Ids live 15 minutes (`IDLife`); at most `MaxIDs` (4 x 500). `Lookup` also checks kind and pane. |
| `Limiter` | Token bucket for requests and bytes. |

Constants: `MaxCandidateBytes` 4096, `MaxComponents` 64, `ChunkBytes` 256 KiB, `MaxRequestBytes` 64 KiB, `MaxListItems` 500, `RequestsPerWindow` 30 per 10 s, `BytesPerMinute` 20 MiB, `MaxSocketsPerDevice` 3, `MaxOpensPerDevice` 4. Only the file and id limits are enforced inside this package.

## Touches

Read-only. It never writes, renames, dials the network or starts a process (a test holds it to that). Name checks come from [secretname](/packages/secretname.md). Used by `internal/server` (consent, audit log, viewer wiring).

See [artifact root confinement](/security/artifact-root-confinement.md) for the refusal order.
