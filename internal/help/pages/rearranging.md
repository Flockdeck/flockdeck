# Rearranging what is already running

A layout is rarely right first time. The agent you thought was a side errand
turns out to be the one you are watching, and it is in the wrong corner.

Nothing is started or stopped by moving it. A move relocates the pane in the
layout tree, so the process, its conversation, its working directory and its
scrollback all come along — and the new arrangement is saved, so it comes back
next time.

## Dragging a pane

Drag a pane by its header. The pane under the pointer shows what would happen
if you let go there:

- onto the **left, right, top or bottom** of another pane — it goes there,
  joining that row or column rather than nesting a new split inside it;
- onto the **middle** of another pane — the two **exchange places**, and the
  layout keeps its shape and proportions exactly as they were;
- onto **another tab** in the tab bar — it moves into that tab;
- onto the **`+` button** — it gets a tab of its own.

## Dragging a tab

Where along another tab it lands decides what happens:

- onto the **left or right end** — it is reordered to there;
- onto the **middle** — the two tabs are **merged**, and one tab is left
  holding every pane of both. Each keeps the arrangement it had, and the room
  is shared out a column at a time rather than half to each tab: fold five tabs
  in one after another and you get five even columns, not one half the width
  and the rest squeezed into slivers you cannot grab a divider in;
- onto the **`+` button** — it goes to the end of the bar.

A tab emptied by dragging its last pane away closes itself, and **the pane is
not closed with it**. That is the difference between moving a pane out and
closing it.

## Putting a tab back in order

Every move here is relative — beside this pane, past that one — and enough of
them leaves a tab with panes too narrow to grab a divider in or drop anything
into. [[action:tilePanes]] is the way back: it lays the tab's panes out in rows of
even columns, in the order they are already in, the same arrangement a fan-out
starts its agents in.

## From the keyboard

[[key:movePaneLeft]], [[key:movePaneRight]], [[key:movePaneUp]] and
[[key:movePaneDown]] move the focused pane past its neighbour in that
direction. The neighbour is chosen by what is on screen rather than by tree
order, so the opposite arrow always puts it back.

The command palette carries the same moves, and [[action:movePaneToNewTab]]
for the one dragging does by dropping a pane on `+`. It also has two that
dragging cannot express as easily: **Move this pane to tab: …** and **Merge
tab into this one: …** for every open tab, and [[action:mergeAllTabs]] for
when the agents you want to watch together are scattered across all of them.

Merging is the reverse of dropping a pane on `+`: what one splits apart, the
other gathers back up.
