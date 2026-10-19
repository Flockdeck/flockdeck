---
type: Package
title: helpers
description: Downloads, verifies, installs, supervises and opens helper apps (small local programs shipped as signed release archives).
resource: internal/helpers/doc.go
tags: [helpers, supply-chain, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/helpers/doc.go, internal/helpers/install.go, internal/helpers/supervisor.go, internal/helpers/logwriter.go, internal/helpers/extract.go, internal/helpers/catalogue.go, internal/helpers/fetch.go, internal/helpers/layout.go]
---
A helper is a separate process with an allowlisted environment and no access to the control server, hooks server or instance token. The catalogue is compiled into the binary (`Catalogue()`, `Lookup`); today it holds one entry, "lens", whose job is to show agent transcripts in a local page.

## Main types

| Type | Role |
|---|---|
| `Entry` | Catalogue item: id, source URL, binary name, `MinVersion`, args and env templates (`{port}`, `{host}`, `{data_dir}`, `{allowed_hosts}`), banner regexp, ready/health paths, "allows" lines, size caps |
| `Installer` (`NewInstaller(Options)`) | Plans and performs installs; `Plan`, `SignatureError`, `MismatchError`, `ChecksumError` |
| `Store` (`DefaultStore`) | The apps directory layout, see [helper install layout](/data/helper-install-layout.md) |
| `Supervisor` (`NewSupervisor(Config)`) | Starts, health-polls, restarts and stops helpers; `Status`, `State` (stopped, starting, running, stopping, unresponsive, failed), `Timings` |
| `RotatingWriter`, `TailLines` | Helper log file with `.1` to `.3` rotation |
| `ExtractRules`, `Extract`, `ArchiveError` | Safe unpacking |

## Supervisor behaviour (defaults)

Banner within 20 s; poll ready every 250 ms for up to 30 s; health every 10 s, unresponsive after 3 failures; stop grace 30 s (CTRL_BREAK on Windows through a hidden `helpers ctrl-break` subcommand); restart backoff 1 s, 2 s, 4 s; more than 3 crashes in 5 min means failed; 3 port tries. On start it verifies the port owner where the OS can say (Windows, Linux); status `owner` is verified or unverified.

Security details: [helper supply chain](/security/helper-install-verification.md). Uses [store](/packages/store.md) for the state directory.
