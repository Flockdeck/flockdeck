# Panes and tabs

A pane is one terminal running one agent. Tabs hold panes, and a tab can hold
one or several, split however you like.

## Making them

| Keys | What it makes |
| --- | --- |
| [[key:newAgentTab]] | A tab with one agent in it |
| [[key:newShellTab]] | A tab with a plain shell — no agent, no hooks |
| [[key:splitRight]] | An agent beside the focused pane |
| [[key:splitDown]] | An agent below the focused pane |
| [[action:splitRightShell]] | A shell beside the focused pane |

A shell pane is an ordinary terminal in the same directory. It is there for
the `git`, `npm` or `go` command you want to run yourself while the agents
work, and for watching a build — which is why it can be split in beside an
agent as well as opened in a tab of its own.

## Working in them

Click a pane to give it the keyboard. Everything you type goes to the agent in
it — that is the point, and it is why this application's own shortcuts are all
on <kbd>Ctrl+Shift</kbd>, which agents do not use.

- Drag the divider between two panes to resize them.
- [[key:zoomPane]] gives the focused pane the whole tab, and gives it back.
  The others keep running; they are simply not on screen.
- [[key:findInTerminal]] searches the focused terminal's scrollback.
- [[key:closePane]] closes a pane and stops the agent in it.

The buttons in a pane header do the same things: fan out, include in
broadcast, restart, zoom, close. Double-click a tab to rename it.

## Restarting

[[action:restartPane]], in the command palette and in the pane header, stops the
process and starts it again in the same directory — resuming the same
conversation, because a pane and its conversation are one identity. Use it
when an agent has wedged itself, not to clear the screen.

A pane whose process exits covers its terminal with a **Restart** button
rather than leaving a dead black rectangle.

## Font size

[[key:fontUp]] and [[key:fontDown]] change the terminal font size for every
pane; [[key:fontReset]] puts it back.
