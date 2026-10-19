# Security boundaries

* [API key handling](api-key-handling.md) - How stored and environment API keys are resolved, written and kept out of logs and UI.
* [Artifact root confinement](artifact-root-confinement.md) - Rules that decide whether a file may be shown to a remote device by the read-only artifact viewer.
* [Remote artifacts gates](artifacts-socket-gates.md) - The conditions a paired device must meet before the artifacts socket serves it anything.
* [Auto-review of read-only Bash calls](auto-review-read-only.md) - How internal/review decides a PreToolUse Bash call is safe to allow without a permission prompt.
* [Baton approval for another provider](baton-approval.md) - A baton leaving its provider needs a user click in a local window; the mechanism and its stated limits.
* [Baton command-line secrets](baton-command-line-secrets.md) - Which command-line flags and arguments the baton scrubber reads as passwords, and how it reads shell words.
* [Baton entropy detector](baton-entropy-detector.md) - The third scrubbing layer, which removes long random-looking runs while keeping paths, URLs, identifiers and hashes.
* [Baton scrubbing and prompt fencing](baton-scrubbing.md) - The layers that remove secrets from a baton and keep its text from being read as instructions or markup.
* [Baton secret shapes](baton-secret-shapes.md) - The second scrubbing layer's own patterns for config files, headers, URLs, cookies and code, and the exemptions that keep placeholders and amounts.
* [Chat client key and endpoint safety](chat-api-key-handling.md) - Rules in the chat client and agent catalog that keep API keys from being sent to the wrong host or printed.
* [Claude Code folder trust inheritance](claude-folder-trust-inheritance.md) - Flockdeck copies an existing trust answer from one directory to a new worktree and never invents one.
* [Paired device access roles](device-access-roles.md) - Per-device full or viewer role stored in prefs, failing closed to viewer.
* [Git program scan before commit, push, pull, fetch](git-program-scan.md) - Flockdeck lists the programs a repository's git config and hooks would run and warns when they are new or changed.
* [Helper install and launch verification](helper-install-verification.md) - Signature, hash, version, archive and pre-start checks that keep a downloaded helper from doing more than asked.
* [Hook server loopback and run token](hook-server-token.md) - How the pane callback server limits who can post events and commands, and what it does not prove.
* [Host header allowlist](host-header-guard.md) - The loopback listener refuses any request whose Host is not 127.0.0.1, localhost or an operator-added name.
* [Jev data handling](jev-data-handling.md) - What may be sent to TypeSafe's Jev API, which key is used, and the guards in the client and gate.
* [Jev status assist privacy limits](jev-status-assist-privacy.md) - The only path that sends terminal output to a third party, gated by a user setting and a key, redacted and rate limited.
* [Local channel access control](local-channel-access.md) - Operating-system and peer checks that restrict the Unix socket or named pipe to the same user.
* [Session token, cookie and one-time window link](local-token-and-window-link.md) - How a local window proves it may drive the server: a per-run token, a per-port HttpOnly cookie and a single-use link.
* [Provider and gateway detection for batons](provider-gateway-detection.md) - How Flockdeck decides which company an agent sends data to, failing to "not known" when settings cannot be read.
* [Recording redaction and secret-file withholding](recording-redaction.md) - Best-effort pattern redaction applied before any transcript line is written and again before any line is served to a client.
* [Relay address validation](relay-address-validation.md) - Rules that keep the host token from being sent to an unsafe or wrong relay address.
* [Relay trust and tunnel authorisation](relay-trust-and-e2e.md) - How requests through the relay tunnel are authorised, what the relay can read, and where end-to-end encryption applies.
* [Release signature verification](release-signature-verification.md) - How the updater trusts a release: compiled-in Ed25519 keys, signed manifests and checksums, SHA-256 on every download.
* [Routing policy source](routing-policy-source.md) - Routing policy is read only from the user's agents.json, never from files inside a repository.
* [Spawn limits, approved targets and inherited auto-review](spawn-approval-and-helper-limits.md) - Caps on agent-started helpers and checks that what a user approved is what runs.
* [State directory permissions](state-dir-permissions.md) - The config directory is created 0700 and state files 0600 because they hold the local server token.
* [Task board permissions](task-board-permissions.md) - The transition table and capacities that decide who may move, assign, edit or prune a task.
* [Team sender labels and notice text](team-sender-labels.md) - How agent-chosen names are sanitised so an agent cannot pass for the user or Flockdeck, or smuggle text into notices.
* [Terminal end-to-end encryption](terminal-e2e-encryption.md) - Per-socket authenticated key exchange and AEAD framing so the relay cannot read remote terminal traffic.
* [Action endpoints need the token in the URL](url-token-endpoints.md) - /health, /open, /quit, /window, /remote/reload and /helpers/* require ?t= and POST, so cookies and relay requests cannot reach them.
* [Viewer command and message gate](viewer-command-gate.md) - Default-deny filtering of control commands, outgoing messages, snapshots and terminal input for viewer devices.
* [WebSocket origin check](websocket-origin-check.md) - The control, terminal and artifacts sockets accept only pages loaded from the server's own address.
