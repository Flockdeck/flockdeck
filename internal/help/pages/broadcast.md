# Broadcast and the prompt bar

Some instructions are for every agent at once — "run the tests and fix what
breaks", across five worktrees. The prompt bar is how you say a thing once,
and broadcast decides who hears it.

## The prompt bar

[[key:promptAll]] opens a single-line prompt at the bottom of the window.
Compose the instruction there and press <kbd>Enter</kbd> or **Send**: it is
delivered as one submitted message rather than typed key by key. <kbd>Esc</kbd>
or **Cancel** closes it without sending anything.

You get to read the sentence before six agents act on it, and a typo is yours
to fix rather than theirs to interpret.

## Who receives it

With broadcast off, the prompt goes to the focused pane, and to any panes you
have added with their `⇉` button. [[key:toggleBroadcast]] — or **Broadcast** in
the top bar — turns broadcast on, and then it goes to every pane in the
**broadcast set**: by default every agent in the tab on screen. While
broadcast is on and the set holds more than one pane, the bar's label says how
many it will reach.

The `⇉` button in each pane header adds that pane to the set or takes it out.
Membership is shown in the pane header even while broadcast is off. Once you
have picked panes by hand the set stays as you made it, through broadcast
being turned off and on, rather than following you from tab to tab.

The focused pane is always included, so a prompt never goes somewhere you
cannot see.

Broadcast only decides where the prompt bar sends. What you type into a pane
goes to that pane alone.
