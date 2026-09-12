# Broadcast and the prompt bar

Some instructions are for every agent at once — "run the tests and fix what
breaks", across five worktrees. The prompt bar is how you say a thing once,
and broadcast decides who hears it.

## The prompt bar

[[key:promptAll]] opens the prompt bar at the bottom of the window. Compose
the instruction there and press <kbd>Enter</kbd> or **Send**: it is delivered
as one submitted message rather than typed key by key. <kbd>Esc</kbd> or
**Cancel** closes it without sending anything, and what you had written is
still there when you open it again.

<kbd>Shift</kbd>+<kbd>Enter</kbd> starts a new line, so an instruction can be
a list — "1. add tests", "2. run them" — and the bar grows to show all of it.
A list pasted in keeps its lines. <kbd>↑</kbd> on the first line and
<kbd>↓</kbd> on the last bring back the prompts you sent before; on the lines
between, they move from line to line.

You get to read the sentence before six agents act on it, and a typo is yours
to fix rather than theirs to interpret.

## Prompts of several lines

A prompt of several lines reaches a pane as one message when the program in
it accepts pasted text — it asks the terminal for bracketed paste, as Claude
Code does — because the bar hands the lines over the way a paste would, and
presses <kbd>Enter</kbd> once, after the last.

A program that does not accept pasted text gets the prompt typed, and there a
new line is an <kbd>Enter</kbd>: each line arrives as a message of its own.
If a pane takes your lines one at a time, send it one line, or write the
prompt as one.

## Who receives it

With broadcast off, the prompt goes to the focused pane, and to any panes you
have added with their `⇉` button. [[key:toggleBroadcast]] — or **Broadcast** in
the rail — turns broadcast on, and then it goes to every pane in the
**broadcast set**: by default every agent in the tab on screen. Whenever the
prompt will reach more than one pane, the bar's label says how many, as in
**Prompt → 3 panes**. That includes panes you added with `⇉` while broadcast
is off.

The `⇉` button in each pane header adds that pane to the set or takes it out.
Membership is shown in the pane header even while broadcast is off. Once you
have picked panes by hand the set stays as you made it, through broadcast
being turned off and on, rather than following you from tab to tab.

The focused pane is always included, so a prompt never goes somewhere you
cannot see.

Broadcast only decides where the prompt bar sends. What you type into a pane
goes to that pane alone.
