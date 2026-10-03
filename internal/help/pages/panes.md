# Panes and tabs

A pane is one terminal running one agent, or a plain shell. Tabs hold panes,
and a tab can hold one or several, split however you like.

## Making them

| Keys | What it makes |
| --- | --- |
| [[key:newAgentTab]] | A tab with one agent in it |
| [[key:newShellTab]] | A tab with a plain shell (no agent, no hooks) |
| [[key:splitRight]] | An agent beside the focused pane |
| [[key:splitDown]] | An agent below the focused pane |
| [[action:splitRightShell]] | A shell beside the focused pane, from the command palette |

Each of those takes the project's default agent, which keeps making a pane one
keystroke. To choose a different agent or a different model, click the `▾`
beside the `+` that opens a new tab, or take the two picker entries out of the
command palette; [Agents and models](#agents) is the page for all of it. The pane header then names what it got, beside the branch.

A shell pane is an ordinary terminal in the same directory. It is there for
the `git`, `npm` or `go` command you want to run yourself while the agents
work, and for watching a build. That is why it can be split in beside an
agent as well as opened in a tab of its own.

## Working in them

Click a pane to give it the keyboard. Everything you type goes to the agent in
it. That is the point, and it is why almost all of this application's own
shortcuts are on <kbd>Ctrl+Shift</kbd>, which agents do not use.

- Drag the divider between two panes to resize them, or reach it with
  <kbd>Tab</kbd> and use the arrow keys, and <kbd>Home</kbd> to share the room
  equally again.
- [[key:zoomPane]] gives the focused pane the whole tab, and gives it back;
  so does double-clicking the pane's header. The others keep running; they
  are simply not on screen.
- [[key:findInTerminal]] searches the focused terminal's scrollback.
- Select text with the mouse and <kbd>Ctrl+C</kbd> copies it; <kbd>Ctrl+V</kbd>
  pastes. With nothing selected, <kbd>Ctrl+C</kbd> goes to the agent, as it
  always did. [Keyboard shortcuts](#shortcuts) has the rest, and macOS.
- [[key:closePane]] closes a pane and stops the agent in it, without asking.
  It also ends what was started in the pane and is still running there: a
  shell's background jobs, a server an agent left going, a job that ignores
  the hangup. A program with windows of its own (a browser, an editor
  started with `code .`) stays open, as it does when a terminal is closed,
  and so does what it runs, such as the terminals in that editor.
  On macOS a background job that ignores the hangup (`nohup`) keeps running.
  Closing a tab's last pane closes the tab, and the `×` on a tab, or a click
  on it with the middle button, closes every pane in it.

The buttons in a pane header, in order: fan out (`⑂`), save the plan as a
todo (`☑`), include in broadcast (`⇉`), auto-review (`✓`), restart, zoom,
export transcript, reveal transcript, record, lock, close. Auto-review is off
until you turn it on, pane by pane (the ✓'s tooltip counts the commands it has
let through unasked so far), and the pane keeps the setting across a
restart of Flockdeck: see [Knowing who needs you](#status). Record is off
until you turn it on too; export, reveal and record are not shown on a shell
pane, which has no conversation, and reveal works only in a window on the
machine Flockdeck runs on: see [Recording a pane](#recording).

## One pane, several windows

A pane has one terminal however many windows show it (a second window, a
phone over [remote access](#remote)), and that terminal is the size of the
window last used on it: the one you focus or type in. Every other window draws
it at that same size, so what the agent prints lands where it should. Where
that is larger than the pane's room in a window, the window shows the part of
it nearest the bottom left, where the latest lines are, with a note such as
**Viewing 120×40 · fit to this window**; click it to size the pane for this
window instead. A pane's terminal is never
made smaller than 20 columns by 5 rows or larger than 500 by 200.

## Locking a pane

[[action:lockPane]], in the command palette and as the padlock in the pane
header, locks a pane so it cannot be closed by accident. The same action
unlocks it again, and the palette names it **Unlock pane** while the pane is
locked. A locked pane shows a padlock and the word "Locked" in its header, and
while it is locked:

- [[key:closePane]] and the `×` in its header do nothing but say the pane is locked.
- Its tab cannot be closed, by the `×` on the tab or the middle button, while
  any pane in it is locked, and neither can a project that holds one.
- [[action:closeFinishedPanes]] leaves it open and says how many it left.
- `flockdeck close` refuses it, and `-force` does not change that: `-force`
  is for a pane still working, and a lock is something only you undo.
  `flockdeck close -finished` skips it and reports how many it skipped.

[[action:restartPane]] still works on a locked pane and leaves it locked. The
lock is kept with the layout, so it survives a restart of Flockdeck, and it
moves with the pane when you drag it to another tab. Quitting Flockdeck is
not closing a pane, so a lock does not stop it.

## Naming a tab

A tab names itself after the first thing its agent is asked. To call it
something else, double-click it, press F2 while the keyboard is on it, or
choose **Rename this tab…** in the command palette; a name you give it stays, whatever the agent is asked next. To go
back, rename it to an empty name or choose **Use the automatic title** in the
same dialog: the tab takes the title it would have had if it had never been
renamed. Either way the name is kept with the layout.

## Restarting

[[action:restartPane]], in the command palette and in the pane header, stops the
process and starts it again in the same directory, with the same agent and
model. It resumes the same conversation where that agent can, because a pane and
its conversation are one identity. Use it when an agent is stuck or has wedged
itself, not to clear the screen.

A pane whose process exits covers its terminal with what happened and two
buttons, **Restart** and **Close pane**, rather than leaving a dead black
rectangle.

## Font size

[[key:fontUp]] and [[key:fontDown]] change the terminal font size for every
pane; [[key:fontReset]] puts it back.
