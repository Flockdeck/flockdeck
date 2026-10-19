# Developer tools

* [cmd/internal/controlsock](controlsock.md) - Shared helper for the dev tools that read a running instance's control socket.
* [ctl](ctl.md) - Sends JSON commands to a running instance over its control WebSocket.
* [deploydrift](deploydrift.md) - Reports sibling repos whose main branch has shipping changes no version tag has picked up, and syncs one tracking issue.
* [design tokens](design-tokens.md) - design/ folder holding tokens.css, the Go test helper for it and the Node contrast checker.
* [docgen](docgen.md) - Generates the documentation site from the in-app help plus hand-written self-hosting pages.
* [hooktest](hooktest.md) - Starts one Claude pane and prints every status transition reported back through the hook server.
* [portproxy](portproxy.md) - Fixed-port TCP forwarder sidecar for the Kubernetes deployment.
* [release](release.md) - Cross-builds, packages, checksums and signs Flockdeck releases in the format the updater reads.
* [sitegen](sitegen.md) - Generates the public marketing site as plain files from this repository.
* [statedump](statedump.md) - Prints what a running instance reports about its panes.
* [treedump](treedump.md) - Prints the tab and split structure a running instance is showing.
