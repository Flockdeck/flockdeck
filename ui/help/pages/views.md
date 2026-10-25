# The team views

The switch above the main area chooses how the project is drawn: **Team**,
**Dependencies**, **Board**, **Changes** and **Replay**. Team is the one the
window opens on. A pane's own terminal and conversation are in the
drawer, which any row or card can open.

## Team

An outline with one row for each agent. A helper is indented under the agent that
started it, and within a level the rows that need you come first, then ended
panes, then working ones, then the rest. A row shows the agent, its role and
task, what it is doing now, how long it has been in that state, and, on a team's
manager, what the team has cost.

A row with an amber bar is waiting on you for a permission or a question. A red
bar is a pane that ended. Chips on a row say what else is going on: a file
overlap with another agent, what the agent is waiting on, messages not yet read,
and a quiet "stuck" for a working agent that has written nothing to its terminal
for a while. A chip that names another agent is a link: hovering or focusing it
lights both rows.

The outline is one tab stop. The arrow keys move between rows, <kbd>Home</kbd>
and <kbd>End</kbd> go to the ends, and <kbd>Enter</kbd> or <kbd>Space</kbd> opens
the agent in the drawer. <kbd>Esc</kbd> closes the drawer and puts the keyboard
back on the row.

## Dependencies

The team's work as a tree of what blocks what, built from the tasks' `depends`
lists. Work at the top is finished last. The **Critical path** is the chain of
tasks that sets when the team can finish; times show only when the tasks carry an
estimate, and without one the path is the longest chain. A task with nothing
open blocking it and no owner is marked **ready**, and a task that appears under
more than one blocker is drawn in full once and then folded as "see above".

Under the tasks are the agents that wait on each other. If they wait in a ring,
none can move: the view names them as an N-way loop and has a button to ask the
manager to break it. With several teams, a team's view lists the tasks it runs,
the tasks that block them and the tasks that wait on them directly. Rows move
like the Team outline.

## Board

The project's tasks in a column for each state. A card shows the id, title, owner,
branch and number of notes, and opens the agent that owns the task, or the
[Team dialog](#agents) when that agent is not open. **Add and manage tasks…**
opens the dialog, where tasks are added, assigned and moved. A very long board
lists the newest tasks and says how many it left out.

## Goal tabs

Each team has a tab along the top, named after its goal, and a **Solo** tab holds
panes in no team. A tab shows how many of its agents need you, or a dot for
whether it is working. Choosing a tab limits the views to that team.

- **Rename** by double-clicking a tab, or from the **+** menu. An empty name
  puts the goal's own back. Names are at most 80 characters.
- **Reorder** by dragging a tab, or with **Move left** and **Move right** in the
  **+** menu.
- **Archive** with the × on the tab. This hides the tab in this window and
  nothing else: the team keeps working, and a tab that gains an agent needing you
  puts a dot on the **+** menu. **Archived goals** in that menu bring it back.

Names, order and archived goals are kept on the machine Flockdeck runs on and are
not sent to a window reached through the [relay](#remote), which has no goal tabs.
