# Answering a terminal pane's question

Claude Code asks the person with its `AskUserQuestion` tool. In a terminal pane that is claude's own screen, drawn in
the terminal, with no mouse. The drawer shows the same question as a form (see `docs/structured-pane.md`, Questions)
and answers it by typing into that screen. The code is `ui/src/drawer/tuiQuestion.js`; its tests run against a fake of
the screen (`ui/test/tuifake.mjs`) built from the capture below.

## What claude's screen does

Captured with claude 2.1.325 on Windows, in a pseudo-terminal 110 columns wide, with an isolated config directory and a
prompt that made it call the tool once with three questions: single choice (3 options), multiple choice (3 options),
single choice (2 options).

```
←  ☐ Color  ☐ Extras  ☐ Size  ✔ Submit  →

Pick a color?

❯ 1. Red
     warm
  2. Green
  3. Blue
  4. Type something.
──────────────────────────────────────
  5. Chat about this

Enter to select · Tab/Arrow keys to navigate · Esc to cancel
```

| Key | Single choice | Multiple choice |
| --- | --- | --- |
| Down, Up | Move the `❯` cursor. Rows are the options, then "Type something", then "Chat about this". | Same. |
| Enter | Picks the row and goes to the next question (the tab becomes `☒`). On the text row it takes the typed text. | Toggles the row. It does not move on. |
| Space | Not used. | Toggles the row. On the text row it types a space. |
| Right | Goes to the next question. On the text row it moves the text cursor and does nothing else. | Same. |
| Left | Goes back one question. | Same. |
| Tab | Goes to the next question from an option row, not from the text row. | Same. |
| A digit | Moves the cursor to that row, without picking. | Toggles that option, cursor stays. On the text row it is text. |
| Typing | On the text row it fills the row ("Type something." becomes the text). | Same, and the row's box is ticked. |

A question that was answered shows `✔` after its choice when you go back to it, and the cursor starts on row 1 again.
After the last question comes a review screen:

```
←  ☒ Color  ☒ Extras  ☒ Size  ✔ Submit  →

Review your answers

 ● Pick a color?
   → Red
 ● Pick extras?
   → Salt, Pepper, xyz
 ● Which size?
   → Large

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel
```

Several answers of one question are joined with ", ", the options first and the typed text last.

## What the drawer sends

For each question, from the first: arrows to put the cursor on the row, Enter (single) or Space (multiple) to pick, the
typed text on the text row, then Enter or Right to move on. After the last question, Enter on "Submit answers". No digit
is ever sent, because a digit does a different thing in each of the cases above.

## When the screen is not known

The person may have moved the cursor, answered a question by hand, or typed something. So the drawer reads the
terminal's rows (`screenLines` of the terminal controller) before every key and stops when it does not see what it
expects:

- Nothing is sent unless the screen shows a question screen whose tab count equals the card's, and whose question text
  and option labels equal one question of the card. A screen on a later question is gone back from with Left, one step
  at a time, each step checked.
- The cursor is read from the screen (`❯`) and moved by the difference. After the keys the screen must show the cursor
  on the row asked for. A multiple-choice box is toggled only when its tick differs from the wanted one, and the tick is
  checked afterwards.
- A text row that already holds text stops the run: the drawer cannot clear it.
- The review must list the card's questions with exactly the answers the form holds, or Submit is not pressed. Cancel is
  never pressed.
- Any step that does not show its result within 2.5 seconds stops the run.

A stop leaves the terminal as it is. The card says why and that nothing more was typed, and the person finishes in the
terminal below. Some answers may already have been entered by then; the terminal shows them.

## Who can do this

The form is drawn only in a window on the desk. A window reached through the relay is sent no question (the pane's `ask`
is cleared by the scrub, `internal/server/relayscrub.go`), so it has no form and nothing to send. A viewer device sees the
form shut and types nothing (`isViewer`, and the terminal socket refuses a viewer's input anyway). The keys travel on the
terminal socket, the path the permission buttons already use. No command was added and `paneSend` is not used.

## Not covered

- Claude's "Chat about this" and Esc (cancel) are left to the terminal.
- A different claude version with a different screen will fail the checks above and fall back to the terminal. The
  capture is of 2.1.325 only.
- A question whose option label is cut by the terminal's width is matched by its visible start; a question whose text
  wraps differently is matched after joining its lines.
