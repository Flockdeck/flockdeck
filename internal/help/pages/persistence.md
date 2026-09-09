# What comes back, and what keeps running

Closing the window is not meant to cost you anything. Layouts, projects and
conversations are restored on the next run, and agents can be left running
without a window at all.

## On the next run

Layouts are saved per project. Tabs, splits, proportions and working
directories come back, **every project you had open is reopened**, and each
agent pane resumes the conversation it had rather than starting an empty one.
The project you name on the command line is the one you land in; the rest are
restored around it.

That works because a pane is identified by a session id handed to Claude when
the pane is created, and handed back when it is restored. Pane identity and
conversation identity are the same thing, which is what makes restore
meaningful rather than cosmetic.

A pane only resumes when Claude actually has a transcript for it, so a pane
that was opened but never prompted starts a fresh conversation instead of
dying on restore.

## Detaching

**Detach**, in the command palette, closes the window and leaves every agent
running.

```sh
agent-wrapper -detach   # start that way, with no window
agent-wrapper           # come back to it
agent-wrapper -quit     # stop everything
```

Closing the window normally still quits, so nothing is left running by
accident.

## Reloading the window

Each pane keeps a bounded ring buffer of recent output, so a window that
reconnects or is reloaded replays it into a fresh terminal. Reloading the page
costs you scrollback beyond that buffer, not the conversation.
