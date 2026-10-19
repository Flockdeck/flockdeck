---
type: Package
title: secretname
description: Decides from a file or folder name alone whether its contents are a secret.
resource: internal/secretname
tags: [security, secrets]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/secretname]
---
Leaf package (standard library only) so code that must not start processes or use the network can share it. Used by [artifacts](/packages/artifacts.md) and `internal/jev`.

## API

| Function | Meaning |
|---|---|
| `Component(name)` | One path component is secret. Case-insensitive; trailing dots and spaces ignored; names over `MaxName` (255) bytes count as secret. A name with "=" is also judged by the text after each "=". |
| `Path(rel)` | Any component of a `/` or backslash path is secret, or two folders in a row form a secret pair (for example `.config/gh`). |
| `Folder`, `StrictFolder`, `FolderPair` | Folder-level checks. `StrictFolder` leaves out the plain words private, secret, secrets. |
| `HomeSecret`, `HomeSecretAbove` | Judge paths relative to a home directory. |

## Matching details

- Folding maps every rune whose case orbit holds an ASCII letter to that letter (long s, Kelvin sign, dotted I) and expands ligatures and sharp s; default-ignorable code points are dropped.
- Credential and browser-store names are stored as SHA-256 digests with a domain prefix, not as text (an antivirus engine flagged a Windows build for a readable list). Tables in `hashes_gen.go` are generated from `testdata/names.txt`; a test fails if they disagree (`go generate`).
- It is a name check only. A secret under a plain name is not caught.
