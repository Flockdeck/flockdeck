---
type: Developer Tool
title: graphfixture
description: Serves a front end and a fake control socket that plays one of the five week-10 graph fixtures.
resource: cmd/graphfixture
tags: [dev-tool, control-socket, graph]
timestamp: 2026-10-21T00:00:00Z
code_paths: [cmd/graphfixture/main.go, cmd/graphfixture/server.go, cmd/graphfixture/fixture.go]
---
Development aid, not shipped.

```
go run ./cmd/graphfixture -fixture 1            # the old front end
go run -tags next ./cmd/graphfixture -fixture 1 # the new shell
```

Flags: `-fixture` (1 to 5, or the file name), `-fixtures` (the directory of the fixtures, found above the working directory by default) and `-list`. It prints the front end it carries, the fixture and a link with a random token, and binds loopback on a port the system chooses.

The front end is the build's `webui.FS()`: the default build serves the old one, a build with `-tags next` the new shell over it. The fake `/ws/control` greets a window with the compiled-in key table and a state built from the fixture's nodes (one project, one tab holding every pane). It answers `graphWatch` and `graphSnapshot` with the fixture's graph message, `paneSend` with the desk's reply or its words for an empty text, one over 16 KiB or a pane the fixture lacks, and `conversationOpen` with a canned page. Other commands are dropped.

It starts no workspace and reads no configuration: it never asks for the configuration or home directory and imports neither the store nor the key files (a test reads its own source for that). There is no `/ws/pty`, so a terminal pane's body stays empty. The node harness of the new shell draws each fixture from the frames the tool sends (`cmd/graphfixture/testdata/render.mjs`).

See also [statedump](/tools/statedump.md).
