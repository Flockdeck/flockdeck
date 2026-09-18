# Todo

A todo is a saved checklist: a plan an agent drafted, kept as steps you start
one at a time, whenever you get to them, rather than all at once. It is meant
to be come back to over days, unlike [Fan out](#fanout), which starts every
task of a plan at once and forgets the run the moment its tab closes.

## Turning a plan into a todo

Ask an agent to plan something the same way you would before [fanning it
out](#fanout) — the plan can come from any pane's conversation, drafted with
the application's ordinary chat. Once it has answered, press
[[action:newTodo]], or the `☑` button in the pane header, beside `⑂`.

The steps are read out of the pane the same way Fan out reads its tasks: out
of a Claude Code agent's own transcript where that is available, off the
screen otherwise. They appear in an editable box, one step per line, with a
title field above it. Edit the list, give it a name, and press **Save as
todo**. Nothing is started yet — saving only writes the checklist down.

## The checklist

Open [[action:todos]] to see every todo saved for the project. Each one shows
its steps with a checkbox and a **Start** button:

- The **checkbox** ticks a step off by hand, in either direction, at any time.
- **Start** opens a fresh agent for that one step, with the step's own text as
  its task — the same way [[action:newAgentTab]] or a fan-out row does,
  optionally in a git worktree of its own. Started this way, several steps of
  one todo get worktrees grouped under the todo's own name in `git branch` and
  the worktree list, rather than a dozen unrelated branches that happen to
  have come from the same checklist.
- Editing a step's text, or dragging it to reorder it, is saved back the same
  way the plan was saved in the first place.

A step you start is not removed from the list, and starting it a second time
— after a failed attempt, or just to try again — keeps every earlier attempt
alongside the new one, so the checklist remembers what was tried and how it
went, not only the latest.

## When a step is done

Closing the pane a step's agent ran in reads its outcome the same way a
settled [fan-out](#fanout) job does, and ticks the step automatically once
that outcome reads as finished. A tick made this way is only ever a starting
point: checking or unchecking a step by hand always has the last word, so a
step you know is really done, or really is not, stays exactly as you left it
regardless of what its agent's pane said on the way out.

## Why not just fan out again

Fan out is for work you want started right now, all of it, in parallel panes
you watch together. A todo is for a plan you are working through over time —
one step today, the next tomorrow — where what has already been done, and
what has not, needs to survive you closing the window. Saving a plan as a
todo does not start anything; fanning one out never remembers it afterwards.
