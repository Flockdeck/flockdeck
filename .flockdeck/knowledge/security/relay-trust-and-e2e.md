---
type: Security Boundary
title: Relay trust and tunnel authorisation
description: How requests through the relay tunnel are authorised, what the relay can read, and where end-to-end encryption applies.
resource: internal/server/remote.go
tags: [security, remote, relay, e2e]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/remote.go, internal/server/pty.go, internal/server/artifacts_socket.go, internal/server/viewerrole.go, internal/server/control.go]
---
## Authorisation

A request is "remote" only if `RemoteHandler` marked its context; that handler is served only on the tunnel listener (`ServeRemote`), so loopback cannot claim the mark. A remote request is accepted without the local token: arriving through the tunnel means the relay already checked the browser is a device paired with this account. The browser never holds the local token.

The relay sets `Flockdeck-Remote-Device`, `Flockdeck-Remote-Device-Name` and `Flockdeck-Remote-Origin` headers. The code says the relay strips client-supplied copies. The device id selects the role ([viewer gate](/security/viewer-command-gate.md)) and the e2e key.

The action endpoints ([URL token endpoints](/security/url-token-endpoints.md)) cannot be reached remotely.

## What the relay can read

Both legs are TLS and the relay decrypts to route. The control socket and HTTP assets are readable by it, which is why the snapshot is cut down for viewers, artifact settings are blanked in remote `prefs`, and sensitive commands are desk-only. The terminal and artifacts sockets are end-to-end encrypted with `internal/e2e`: a P-256 identity per side, handshake as the socket's first two frames, then sealed frames. See also [terminal e2e encryption](/security/terminal-e2e-encryption.md).

| Socket | If a key is missing on either side |
|---|---|
| `/ws/pty` | served unencrypted |
| `/ws/pty`, handshake fails | closed (no downgrade) |
| `/ws/artifacts` | refused with reason `key` |

## Other limits on remote windows

No quit, restart or detach; no starting helper apps; team panel refused; update and version commands refused; a baton approval is never sent to a remote window.

Until the scrub task (X1) is done, a remote window is sent no conversation text: the conversation commands are refused whatever the device role (`remoteTextCommands`), the conversation and graph messages are dropped or emptied at `controlClient.write`, and the state push and agents list clear the live line, opening prompt, last reply, questions, permission text and background work text (`internal/server/relayscrub.go`). The roster and the reason a pane needs the person stay. The terminal socket is sealed and is not covered.
