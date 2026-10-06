# Todo

A todo is a saved checklist: a plan an agent drafted, kept as steps you start
one at a time, whenever you get to them, rather than all at once. It is meant
to be come back to over days, unlike [Fan out](#fanout), which starts every
task of a plan at once and forgets the run the moment its tab closes.

## Turning a plan into a todo

Ask an agent to plan something the same way you would before [fanning it
out](#fanout). The plan can come from any pane's conversation. Once it has
answered, press [[action:newTodo]] in the command palette, or the `☑` button
in the pane header, beside `⑂`.

The steps are read out of the pane the same way Fan out reads its tasks: out
of the agent's own stored conversation where Flockdeck can read it (Claude
Code's, or the built-in chat client's) and off the screen otherwise. They
appear in an editable box, one step per line, under a **Title** field. Edit the list, give it a name, and press **Save as todo**, or
<kbd>Ctrl</kbd>+<kbd>Enter</kbd> in the box. Nothing is started yet: saving
only writes the checklist down. A todo saved without a title is called
"Untitled todo".

## The checklist

Open [[action:todos]], in the rail or the command palette, to see every todo
saved for the project; **+ New todo** there reads the focused pane's plan the
same way. Each todo shows how many of its steps are done, and each step a
checkbox and a **Start** button:

- The **checkbox** ticks a step off by hand, in either direction, at any time.
- **Start** opens a fresh agent for that one step, on the project's default
  agent, with the step's own text as its task. **Start each step below in a
  git worktree of its own**, at the top of the todo, puts it in a worktree
  instead of the project's own checkout; it starts off, and is remembered per
  todo while the window stays open. Steps of one todo started this way get branches grouped under the
  todo's own name, `agent/<todo>/<step>`, in `git branch` and the worktree
  list, instead of a dozen unrelated branches.
- Editing a step's text, or dragging it to reorder it, is saved back the same
  way the plan was saved in the first place.
- **Delete** removes the whole todo, checklist and history, after asking.

Todos are kept on disk with Flockdeck's own state, so they are there after a
restart.

A step you start is not removed from the list, and starting it a second time
(after a failed attempt, or just to try again) keeps every earlier attempt
alongside the new one, so the checklist remembers what was tried and how it
went, not only the latest.

## When a step is done

Closing the pane a step's agent ran in reads its outcome the same way a
settled [fan-out](#fanout) job does (done, needs input, or failed), and the
step's row shows it beside how many attempts it has had. An outcome of done
ticks the step, including one you unticked before running another attempt.
An outcome never unticks one, so a step you ticked by hand
stays ticked whatever its agent's pane said on the way out, and a tick you
think is wrong is yours to take off.

## Why not just fan out again

Fan out is for work you want started right now, all of it, in parallel panes
you watch together. A todo is for a plan you are working through over time,
one step today, the next tomorrow, where what has already been done, and
what has not, needs to survive you closing the window. Saving a plan as a
todo does not start anything; fanning one out never remembers it afterwards.
