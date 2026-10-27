# Configuration and environment variables

* [CLAUDE_CODE_GIT_BASH_PATH](claude-code-git-bash-path.md) - Claude Code's Git Bash location on Windows, read so Flockdeck quotes hook and status-line commands for the same shell.
* [CLAUDE_CODE_POWERSHELL_RESPECT_EXECUTION_POLICY](claude-code-powershell-respect-execution-policy.md) - When set, Flockdeck stops passing -ExecutionPolicy Bypass when it runs a status line command through PowerShell.
* [CLAUDE_CONFIG_DIR](claude-config-dir.md) - Claude Code's own config directory override, read by Flockdeck to find transcripts, trust answers and settings.
* [CODEX_HOME](codex-home.md) - Codex's own config directory override, read by Flockdeck to find config.toml when classifying where an agent sends data.
* [DEPLOY_DRIFT_TOKEN](deploy-drift-token.md) - GitHub token used by deploydrift to read the sibling repositories.
* [FLOCKDECK_AGENT](flockdeck-agent.md) - The id of the agent a pane runs, set in the pane's environment.
* [FLOCKDECK_ALLOWED_HOSTS](flockdeck-allowed-hosts.md) - Extra Host header values the loopback server answers to, for reaching the UI through another name.
* [FLOCKDECK_API_KEY](flockdeck-api-key.md) - Fallback API key used by any agent that has no key of its own.
* [FLOCKDECK_API](flockdeck-api.md) - Loopback address of the instance a pane belongs to; set in each pane's environment.
* [FLOCKDECK_DETACH](flockdeck-detach.md) - Environment form of the -detach flag: keep running without a window.
* [FLOCKDECK_DETACHED](flockdeck-detached.md) - Internal marker the -detach re-launch sets on its child process (Unix builds).
* [FLOCKDECK_DIR](flockdeck-dir.md) - Default workspace directory for the root command; the environment form of -C.
* [FLOCKDECK_FRESH](flockdeck-fresh.md) - Environment form of the -new flag: start with a single pane instead of the saved layout.
* [FLOCKDECK_LAUNCH](flockdeck-launch.md) - Per-start marker in a pane's environment that hooks send back so late events from a previous process can be dropped.
* [FLOCKDECK_NO_WINDOW](flockdeck-no-window.md) - Environment form of the -no-window flag: serve without opening a window.
* [FLOCKDECK_PANE](flockdeck-pane.md) - The id of the pane a process is running in; identifies the caller to the instance.
* [FLOCKDECK_PROJECT and FLOCKDECK_PANE_NAME](flockdeck-project.md) - The pane's project root and display name, set so a shell prompt or script can read them.
* [FLOCKDECK_RELAY](flockdeck-relay.md) - Overrides the default relay address used when enabling remote access.
* [FLOCKDECK_REMOTE_INVITE](flockdeck-remote-invite.md) - Default invitation code for remote enable, for relays that require one.
* [FLOCKDECK_REMOTE_JOIN](flockdeck-remote-join.md) - Default join code for remote enable, to join another machine's account.
* [FLOCKDECK_REMOTE_NAME](flockdeck-remote-name.md) - Default machine name for remote enable, for first-boot scripts.
* [FLOCKDECK_SHELL_FIRST](flockdeck-shell-first.md) - Environment form of the -shell flag: open the first pane as a shell.
* [FLOCKDECK_SIGNING_KEY](flockdeck-signing-key.md) - Release signing key read by `release -sign`.
* [FLOCKDECK_SOLO](flockdeck-solo.md) - Environment form of the -solo flag: always start a new instance.
* [FLOCKDECK_START_AGENT](flockdeck-start-agent.md) - Default agent id new panes start as; the environment form of -agent.
* [FLOCKDECK_TESTISO_REAL_CONFIG and FLOCKDECK_TESTISO_REAL_HOME](flockdeck-testiso-real-dirs.md) - Variables test binaries use to pass the developer's real config and home directories to child test binaries.
* [FLOCKDECK_TOKEN](flockdeck-token.md) - Shared secret a pane uses to authenticate to its instance.
* [FLOCKDECK_UPDATE](flockdeck-update.md) - Set to "off" to stop background checks for new releases.
* [PERCH_ pane variables](perch-pane-env.md) - Deprecated PERCH_ names for the FLOCKDECK_ variables a pane carries.
* [routing.crossAgent](routing-cross-agent.md) - Lets a routing rule move a spawned helper to another agent, not only to another model.
* [routing.floor](routing-floor.md) - The lowest model tier routing may choose.
* [routing.mode](routing-mode.md) - Turns model routing off or on, per installation or per project.
* [routing.strategy](routing-strategy.md) - Whether work that no rule matches is left alone (balanced) or sent to the cheapest allowed tier (cost).
* [TYPESAFE_API_KEY](typesafe-api-key.md) - TypeSafe's own variable, used as the Jev API key when none is set in Settings.
* [XDG_SESSION_ID](xdg-session-id.md) - Names the logind session that internal/idle asks about on Linux.
