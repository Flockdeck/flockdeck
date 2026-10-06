# Keyboard shortcuts

Everything not listed here goes to the focused agent, which needs the rest of the
keyboard for itself. Press `F1` in the window for the same list, alongside the rest
of the help. The [shortcuts help page](../internal/help/pages/shortcuts.md) explains
the choices behind it and how to change a binding.

This table is generated from the one key table in `internal/help/keys.go`, which
the command palette and the in-app help are also drawn from. After changing that
table, run `go test ./internal/help -run TestKeysDocShortcuts -update`.

<!-- shortcuts:start -->

## Panes

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+D` | Split right (agent) |
| `Ctrl+Shift+E` | Split down (agent) |
| Command palette | Split right (choose agent)… |
| Command palette | Split right (shell) |
| `Ctrl+Shift+←` | Move pane left |
| `Ctrl+Shift+→` | Move pane right |
| `Ctrl+Shift+↑` | Move pane up |
| `Ctrl+Shift+↓` | Move pane down |
| Command palette | Move pane to a tab of its own |
| Command palette | Tile these panes evenly |
| `Ctrl+Shift+Z` | Zoom pane |
| Command palette | Restart pane |
| Command palette | Lock pane |
| Command palette | Start recording |
| Command palette | Export transcript |
| Command palette | Reveal transcript |
| `Ctrl+Shift+Y` | Pane info |
| `Ctrl+Shift+W` | Close pane |

## Tabs

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+T` | New agent tab |
| Command palette | New agent tab (choose agent)… |
| `Ctrl+Shift+N` | New shell tab |
| `Ctrl+Tab` | Next tab |
| `Ctrl+Shift+Tab` | Previous tab |
| `Alt+1 … Alt+9` | Select tab by number |
| Command palette | Merge every tab into this one |
| Command palette | Move tab left |
| Command palette | Move tab right |

## Agents

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+B` | Toggle broadcast |
| Command palette | Add this pane to broadcast, or take it out |
| `Ctrl+Shift+P` | Prompt all panes |
| `Ctrl+Shift+X` | Fan out — turn this pane's plan into agents |
| Command palette | Make baton: hand this pane's work to another agent |
| Command palette | Fan-out history — past jobs in this project |
| Command palette | New todo — save this pane's plan as a checklist |
| Command palette | Todos — this project's saved checklists |
| `Ctrl+Shift+A` | All agents across projects |
| Command palette | Close finished panes — idle agents and cleanly exited panes, in every open project |
| Command palette | API keys… |

## Git

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+G` | Worktrees |
| `Ctrl+Shift+S` | Review changes, commit and push |

## Finding your way

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+K` | Command palette |
| `F6` | Move to the next part of the window |
| `Shift+F6` | Move to the previous part of the window |
| `Ctrl+Shift+F` | Find in terminal |
| `Ctrl+Shift+L` | Find pane |
| `Ctrl+Shift+R` | Resume a past conversation |
| `Ctrl+Shift+O` | Projects |
| `F1` | Help |

## The window

| Keys | Action |
| --- | --- |
| `Ctrl+,` | Settings |
| `Ctrl+B` | Expand or collapse the rail |
| `Ctrl+=` | Increase font size |
| `Ctrl+-` | Decrease font size |
| `Ctrl+0` | Reset font size |
| Command palette | Flockdeck Remote… |
| Command palette | Helper apps… |
| Command palette | Open recordings folder |
| Command palette | Detach — close the window, leave agents running |
| Command palette | Quit — stop every agent in every project |

<!-- shortcuts:end -->

Panes are focused by clicking, resized by dragging the divider between them (or from
the keyboard: `Tab` to it, then the arrow keys, and `Home` to share the room
equally), moved by dragging their header, and closed, restarted, zoomed or locked
from the buttons in their header. A locked pane cannot be closed until you unlock it.
In any dialog `Esc` closes it, and the arrow keys and `Enter` work through its list.
