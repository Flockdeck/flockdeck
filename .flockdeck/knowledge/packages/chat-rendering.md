---
type: Package
title: chat rendering and width
description: How the chat client wraps a streamed answer to the pane, picks out markdown, counts display width and neutralises control characters.
resource: internal/chat/render.go
tags: [chat, terminal, rendering, width]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/render.go, internal/chat/width.go, internal/chat/termsize_unix.go, internal/chat/termsize_windows.go, internal/chat/termsize_other.go]
---
# Chat rendering

Part of [chat](/packages/chat.md). A `printer` draws everything the client shows: the streamed answer, notices, questions and the input prompt.

## Width

`resolveWidth` takes the first of: `FLOCKDECK_COLUMNS` (an integer of at least 20, see [env](/config/flockdeck-columns.md)); `terminalWidth()` when it is 20 or more; `COLUMNS` (at least 20); else `defaultWidth` (80). `terminalWidth` uses the `TIOCGWINSZ` ioctl on Linux and macOS, and `GetConsoleScreenBufferInfo` on Windows (window right minus left plus one). Anywhere else it returns 0 (`termsize_other.go`). `newPrinter` falls back to 80 for a width under 20.

`displayWidth` counts columns, not runes. Zero width: zero-width joiner, variation selectors U+FE00 to U+FE0F, and Unicode categories Mn and Me. Two columns: the ranges in `wideRanges` (Hangul Jamo, CJK blocks, Hiragana and Katakana, Hangul syllables, fullwidth forms, and the emoji blocks 1F300-1F64F, 1F680-1F6FF, 1F900-1F9FF, CJK extensions). Mixed-width blocks such as dingbats stay at one, so a line wraps early rather than late.

## Colour

`colourWanted` is false if `NO_COLOR` is non-empty, `TERM=dumb`, or stdout is not a console. Otherwise `enableColour()` decides: always true on Linux and macOS; on Windows it turns on virtual terminal processing for the console if it is off. Styles are bold, dim, cyan, blue and red escape codes, and `style` closes every styled write at once with a reset.

## Wrapping and markdown

The printer works a word at a time with one word of lookahead, so text appears as it streams. It handles:

- Heading markers (a word of 1 to 6 `#` only): not drawn, the line is bold.
- List markers (`-`, `*`, `+`, a bullet, or digits followed by `.` or `)`): wrapped lines hang under the item text.
- `**` and a backtick toggle bold and monospace (cyan) across words.
- Fenced code: drawn line by line in cyan, never wrapped; the language after the fence is dropped; an unclosed block is flushed at the end of the message.
- A word starting with `|` makes the rest of the line a table row, drawn as written.
- More than one blank line in a row collapses to one.

`line` draws a whole line in a style. A dim, red or bold line whose first line is wider than the pane is wrapped at spaces by `wrapNotice`; a word wider than the pane stays whole. Other styles, such as tool output, are drawn as they came.

## Control characters

`visible` replaces ESC with the symbol for escape and any other C0, C1 or DEL control character, or invalid UTF-8, with U+FFFD. Tab, newline and carriage return are kept (a lone carriage return becomes a newline in `line`). Model text, tool output and file contents all pass through it, so they cannot set the clipboard, retitle the window or recolour the pane.
