---
type: Security Boundary
title: Artifact root confinement
description: Rules that decide whether a file may be shown to a remote device by the read-only artifact viewer.
resource: internal/artifacts
tags: [security, remote, files]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/artifacts/root.go, internal/artifacts/root_unix.go, internal/artifacts/root_windows.go, internal/artifacts/rootguard.go, internal/artifacts/rootpolicy.go, internal/artifacts/rootpolicy_home.go]
---
Implemented by [artifacts](/packages/artifacts.md) with name checks from [secretname](/packages/secretname.md).

## Open order (`Root.Open`)

1. Length over 4096 bytes, or control or (on Windows) illegal characters: `lexical`.
2. Not inside the root (absolute, `..`): `outside`.
3. More than 64 components, or lexically unsafe (alternate data stream, trailing dot or space, 8.3 short name, UNC or extended prefix, Windows reserved device names): `lexical`.
4. A secret name in any component: `denied`.
5. Walk of the path: no symbolic link, junction or reparse point (even inside the root), no component on another device (Unix), non-ASCII components must be a byte-exact folder entry (Unix, `alias`), only regular files.
6. Open with `os.Root`, then vet the open handle: still regular, link count 1 (`hardlink`), not swapped since the check (`changed`), and on Windows the real long name read from the handle is inside the root and not denied.

Every refusal is `ErrUnavailable` to the caller; the specific `Reason` is for the local log only. Errors from reads and closes are replaced the same way so absolute paths never reach a client.

## Roots (`NewRoot`)

Refused: empty or relative, filesystem or drive root, a home directory or a folder above or holding homes, secret-keeping folders (`.ssh`, `.aws`, `.gnupg`, `.kube`, `~/.config/gh`, browser profiles, below them too), folders holding all programs' settings, system and program folders, places with a secret-named folder on the way. Fails closed when no existing, owned home directory can be found. The root must come from a path the host chose.

## Other limits

- Ids are random, per-socket, 15-minute, tied to kind and pane.
- At most 5 MiB of a file is served.
- Never writes, dials or starts processes.

## Not covered

A secret saved under an ordinary name, or inside a non-secret file, is not caught. The package documentation says the viewer's enabling flow must tell the person this. The server separately keeps a consent flow and an audit log named `remote-artifacts.log`.
