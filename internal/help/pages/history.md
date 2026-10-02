# Past conversations

[[key:history]], or **History** in the rail, lists the conversations stored
for this project and resumes any of them into a new tab.

For each one it shows the opening prompt, how long ago it was touched, how many
entries it holds, its size and the start of its session id. Typing in the
field above the list narrows it to the conversations holding every word typed.
**Resume**, or <kbd>Enter</kbd> on a row, opens it in a new tab and picks it
up where it stopped.

This covers the conversations no pane is currently attached to: the one from
yesterday, or one whose pane you closed. A conversation that *is* open in a
pane is marked **already open** rather than offered twice, because two panes
on one transcript would fight.

The list is read from each agent's own stored conversations rather than from
anything Flockdeck keeps. Claude Code's are listed, so Claude Code
conversations from sessions that had nothing to do with this application are
in it too, and so are those of the model APIs spoken to through Flockdeck's
built-in chat client. A conversation resumed from it opens with the agent that
held it. Other agents' conversations are not listed, since Flockdeck cannot
read where they keep them.
