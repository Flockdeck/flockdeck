---
type: Security Boundary
title: Relay address validation
description: Rules that keep the host token from being sent to an unsafe or wrong relay address.
resource: internal/remote/config.go
tags: [security, remote, tls]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/remote/config.go, internal/remote/enrol.go]
---
`CheckRelay` (used by `RelayURL`, enrolment and move):

- Requires `https`. Plain `http` only for loopback hosts (`localhost`, loopback IPs). Other schemes are rejected. An address without a scheme gets `https://`.
- Rejects the relay's own credentials typed in the address field (prefixes `fdh_`, `fdd_`, `fdp_`, `fdi_`), without echoing them, so they are not sent to DNS.
- Rejects pairing links (`/pair` with `#fdp_...`), a machine page address (`/h/<id>`), a query or fragment, and user info in the address.
- Normalises: lower-case host, default ports removed, trailing slash removed, and the old default host name mapped to the current default.

`Load` applies the TLS rule again to `remote.json`, so a hand-edited `http://` or `ws://` non-loopback relay stops the load instead of sending the token in clear. Relay HTTP calls cap responses at 1 MiB and add the token only as a Bearer header ([remote.json](/data/remote-config-file.md)).
