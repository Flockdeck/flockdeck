# Rearranging what is already running

A layout is rarely right first time. The agent you thought was a side errand
turns out to be the one you are watching, and it is in the wrong corner.

Nothing is started or stopped by moving it. A move relocates the pane in the
layout tree, so the process, its conversation, its working directory and its
scrollback all come along — and the new arrangement is saved, so it comes back
next time.

## Dragging a pane

Drag a pane by its header. While the drag is in progress every other pane
shows what would happen:

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
  holding every pane of both. Each keeps the arrangement it had and takes half
  the room, so two agents started in separate tabs end up side by side without
  either being restarted;
- onto the **`+` button** — it goes to the end of the bar.

A tab emptied by dragging its last pane away closes itself, and **the pane is
not closed with it**. That is the difference between moving a pane out and
closing it.

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
