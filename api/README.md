# The Flockdeck API

What a client of the server (the desktop interface, the same interface through the relay, the phone app) may rely on. The server and the interface are one repository today and are meant to be separable, so the contract is files, not Go.

- `catalog.json`: every command a client sends over `/ws/control`, the frames it sends that are not commands (`frames`: today `clientHello`), and every message the server writes, with who may send each command (`access`), what a relay window is sent of each message (`relay`: `scrub`, `pass` or `deskOnly`), when it appeared (`since`) and whether it may change (`status`). It is generated from the tables the server decides by and a test fails when the file differs from them. To write it again: `API_CATALOG_UPDATE=1 go test -run TestAPICatalogIsCurrent ./internal/server`.
- `fixtures/`: graph pushes recorded from a real server; see its README for what is recorded and what is only built.
- `schema/`: JSON Schemas for the graph push, structured panes and the two recording formats. Each has tests that validate real output against it.

`api/` holds data, no code. The version is `api.major` and `api.minor` in the catalog: adding a command or message raises the minor, removing or changing one raises the major.
