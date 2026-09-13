# Keyboard shortcuts

Everything not listed here goes to the focused agent, which needs the rest of
the keyboard for itself. That is why almost every binding is on
<kbd>Ctrl+Shift</kbd>. The exceptions are the ones no terminal program claims:
switching tabs, the font size, [[key:settings]] for the settings,
<kbd>F1</kbd> for this help, and [[key:nextRegion]] for the rest of the window.

Inside a terminal <kbd>Tab</kbd> belongs to the program running there, so
[[key:nextRegion]] is how the keyboard gets out: it moves between the rail, the
top bar, the focused pane's buttons and its terminal, in that order, and
[[key:prevRegion]] goes the other way. Both work from the prompt bar too.
While a dialog is open the keyboard stays there.

Copying and pasting in a terminal works as it does in Windows Terminal.
<kbd>Ctrl+C</kbd> copies what is selected, and with nothing selected goes to
the program as usual, to interrupt it. <kbd>Ctrl+Shift+C</kbd> copies too, and
<kbd>Ctrl+V</kbd> and <kbd>Ctrl+Shift+V</kbd> paste. On macOS,
<kbd>Cmd+C</kbd> and <kbd>Cmd+V</kbd> copy and paste, and <kbd>Ctrl</kbd>
with a letter always goes to the program.

Actions marked *Command palette* have no binding of their own: press
[[key:palette]], or **Commands** at the right of the top bar, and search for
them by name.

On macOS the bindings are the same, with <kbd>Ctrl</kbd> rather than
<kbd>Cmd</kbd>, except for picking a tab by number: that is
<kbd>Cmd+1</kbd> … <kbd>Cmd+9</kbd> there. <kbd>Option</kbd> with a digit is
how most Mac layouts type characters such as <kbd>[</kbd>, <kbd>|</kbd> and
<kbd>#</kbd>, so it is left to type them.

{{keys}}

Panes are focused by clicking, resized by dragging the divider between them,
moved by dragging their header, and closed, restarted or zoomed from the
buttons in their header. Double-click a tab to rename it; an empty name gives
it back the title it gives itself.

A divider can be resized from the keyboard too: reach it with <kbd>Tab</kbd>,
then the arrow keys move it and <kbd>Home</kbd> shares the room equally. In the
tab bar, <kbd>←</kbd> and <kbd>→</kbd> move between tabs, and <kbd>Home</kbd>
and <kbd>End</kbd> go to the first and the last.

## Inside a dialog

- <kbd>Esc</kbd> closes any dialog, the command palette and the prompt bar, and
  the find bar while you are typing in it. <kbd>Tab</kbd> stays inside an open
  dialog.
- While a dialog is open, the other shortcuts wait until it closes, so nothing
  changes behind it unseen. The font size keys, [[key:palette]] and
  [[key:help]] still work.
- In the command palette, the agent picker and the help's contents,
  <kbd>↑</kbd> and <kbd>↓</kbd> move through the list and <kbd>Enter</kbd>
  takes the one selected.
- In the agent picker, <kbd>→</kbd> opens an agent's models and <kbd>←</kbd>
  closes them, while nothing has been typed to narrow the list.
- In the find bar, <kbd>Enter</kbd> goes to the next match and
  <kbd>Shift+Enter</kbd> to the one before.
