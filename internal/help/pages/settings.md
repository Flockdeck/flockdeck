# Settings

Flockdeck has no settings screen: each thing you can change is changed where it
is used, and kept in one of a handful of files. This page lists every one of
them, how to change it, and where it is kept.

## In the window

| Setting | How to change it | Kept |
| --- | --- | --- |
| The agent and model a pane runs | [[action:splitRightChoose]] or [[action:newAgentTabChoose]] | with the layout |
| Which agent and model a project starts | **Set as default for this project**, at the foot of the picker; to go back to the default for every project, remove the project's entry under `projects` in `agents.json` | `agents.json` |
| An API agent's key | [[action:apiKeys]], or `flockdeck keys set <agent>` | `keys.json` |
| A tab's name | double-click the tab | with the layout |
| Which panes the prompt bar reaches | [[key:toggleBroadcast]], and the `⇉` button in each pane header | until Flockdeck stops |
| Terminal font size | [[key:fontUp]], [[key:fontDown]], [[key:fontReset]] | until Flockdeck is next started |
| Paired devices | [[action:remote]] — **Pair a device**, **Unpair** | on the relay |
| Desktop notifications | the browser's own site settings, for the address in the title bar | the browser |
| Hints under the tab bar | their close button, which sends one away for good | `prefs.json` |

## In the state directory

Everything Flockdeck keeps is in one directory: `%AppData%\flockdeck` on
Windows, `~/Library/Application Support/flockdeck` on macOS, and
`~/.config/flockdeck` on Linux (or `$XDG_CONFIG_HOME/flockdeck` where that is
set).

| File | What it holds |
| --- | --- |
| `agents.json` | Your own agents, changes to the built-in ones, and the default agent and model — for every project under `defaults`, for one under `projects`. **Agents and models** describes it; it is read again each time the picker opens, so editing it needs no restart. |
| `keys.json` | API keys set through Flockdeck. A key exported in the environment is used first. |
| `remote.json` | This machine's enrolment with the relay. `flockdeck remote disable` removes it. |
| `prefs.json` | Whether the help has been opened, and which hints were dismissed. Delete it while Flockdeck is not running to have the help open on the next start and every hint back. |
| `layout-….json` | One per project: its tabs, splits and panes. `flockdeck -new` starts without it. |
| `projects.json` | The recent projects the picker offers. |
| `session.json` | Which projects are reopened on the next start. |
| `error.log` | Why Flockdeck failed to start, when it had no terminal to say so in. |

## In the environment

| Variable | Effect |
| --- | --- |
| `FLOCKDECK_UPDATE` | `off` stops the background check for new releases |
| `FLOCKDECK_BROWSER` | Which browser provides the window, by name or path |
| `FLOCKDECK_RELAY` | Which relay `flockdeck remote enable` uses |
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` | Keys for the API agents, used before anything in `keys.json` |
| `CLAUDE_CONFIG_DIR` | Where Claude Code keeps its own files; Flockdeck follows it to find conversations and folder trust |
| `NO_COLOR` | The built-in chat client draws without colour |

The variables Flockdeck sets in each pane, and the flags that shape one run —
`-agent`, `-new`, `-shell` — are on **The command line**.
