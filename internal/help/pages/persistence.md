# What comes back, and what keeps running

Closing the window is not meant to cost you anything. Layouts, projects and
conversations are restored on the next run, and agents can be left running
without a window at all.

## On the next run

Layouts are saved per project. Tabs, splits, proportions and working
directories come back, **every project you had open is reopened**, each pane
starts the agent and model it had, and each one resumes the conversation it had
rather than starting an empty one. The project you name on the command line is
the one you land in; the rest are restored around it.

That works because a pane is identified by a session id handed to the agent
when the pane is created, and handed back when it is restored. Pane identity
and conversation identity are the same thing, which is what makes restore
meaningful rather than cosmetic.

Two things have to hold for a conversation to come back: the agent has to be
able to reattach one by id, and it has to have written a transcript to
reattach. So a pane opened and never prompted starts a fresh conversation
rather than dying on restore, and a pane running an agent that cannot resume
comes back in the right place, in the right directory, with an empty
conversation. **Agents and models** says which is which.

A layout saved by a build that knew only about Claude is read without a
murmur: its panes come back running Claude Code at whatever model the CLI is
set to, and nobody is asked anything.

## Detaching

[[action:detach]], in the command palette, closes the window and leaves every agent
running.

```sh
flockdeck -detach   # start that way, with no window
flockdeck           # come back to it
flockdeck -quit     # stop everything
```

Closing the window normally still quits, so nothing is left running by
accident.

## Reloading the window

Each pane keeps a bounded ring buffer of recent output, so a window that
reconnects or is reloaded replays it into a fresh terminal. Reloading the page
costs you scrollback beyond that buffer, not the conversation.
