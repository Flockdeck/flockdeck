# Design tokens: "Quiet deck"

One direction for four surfaces: the desktop app, flockdeck.ai, the remote
client and the docs. **The chrome is quiet so the signals can be loud.**
Flockdeck is an instrument panel for a row of agents, and the terminals are the
content. Everything that is not a terminal or a status steps back: one cool
neutral ramp, hairline rules instead of shadows, small corners, no glow, no
gradient, no glass. The only saturated things on screen are the states of
agents and the one place the keyboard is.

This folder is the source of truth. If a stylesheet and this folder disagree,
the stylesheet is wrong.

| File | What it is |
|---|---|
| [`tokens.css`](./tokens.css) | The canonical values as CSS custom properties (`--fd-*`), the status marks, the focus ring and the reduced-motion block. Copy values from here, never from a screenshot. |
| [`check-contrast.mjs`](./check-contrast.mjs) | Reads `tokens.css` and proves every ratio quoted below (243 pairings) and the colour-vision table. Exits 1 on a failure. |
| [`contrast-report.md`](./contrast-report.md) | The full output of that script as tables. The script fails when it is stale. |
| [`tokens.go`](./tokens.go) | Package `design`: the helper a surface's Go test calls to prove its stylesheet carries these values (see "How it is enforced"). |
| [`fonts/`](./fonts) | The licence texts (SIL OFL 1.1) of the two typefaces. The font files ship with each surface. |
| [`multi-agent.md`](./multi-agent.md) | A historical design record, unrelated to the tokens. |

## How it is enforced

Nothing here is advice.

- `node design/check-contrast.mjs --check-report` runs in CI (the
  `design-tokens` workflow, and inside `go test ./design`). It reads
  `tokens.css`, so a value cannot pass here and differ there, and it fails if
  `contrast-report.md` is not what the script prints now.
- `go test ./design` checks that `tokens.css` is well formed (both themes
  complete, the system block equal to the light block), that this folder holds
  no second palette, and that every colour written in this document is a token.
- A surface's own test calls `design.AssertSurface` with the mapping in
  "Token mapping" below, so its `:root` cannot drift from the values here.
- To change a colour: edit `tokens.css`, run `node design/check-contrast.mjs`,
  regenerate the report (`node design/check-contrast.mjs --md >
  design/contrast-report.md`), update this document, and only then change a
  stylesheet.

## Principles

**1. Status colour is earned, never decorative.** Cyan, amber, red and green
appear only where the app is reporting a state or a verdict it actually knows.
Not on a heading, not on a chart series, not on a marketing panel, not to make
a button feel positive. If you want a colour and there is no state behind it,
the answer is the accent or a neutral.

**2. Working is the accent, and green is a verdict.** A run in progress has not
reached a verdict. Activity takes the accent cyan, which is also the colour the
application icon draws the working bird in; only a finished, successful thing
(tests passed, tree clean, lines added) is green (`ok`).

**3. Never colour alone.** A status is a shape, and wherever there is room, a
word. Colour is the third channel, not the first. See "The status marks".

**4. One accent, no second brand hue.** Two accents and the eye starts
assigning meaning to the difference. Emphasis comes from `accent-strong`,
weight and space. (The desktop app lets a user pick a swatch; it changes the
accent and never a status.)

**5. Neutrals carry the UI.** Nearly every surface, edge and glyph is neutral.
Colour is the exception, which is what makes it legible when it appears.

**6. Identity comes from three things.** The drawn status marks; a mono data
layer (anything measured or machine-made is set in JetBrains Mono on every
surface, words in one sans); and the mark's cyan.

Not: a neon hacker terminal, frosted glass, a glow-and-gradient launch page,
brutalist, or a warm document. It does not animate for effect and it does not
float its navigation in a pill.

## Colour

Every ratio is WCAG 2.1 contrast, computed by `check-contrast.mjs`. Text needs
4.5:1; a control edge, a focus ring or a status mark needs 3:1. Names are the
`--fd-*` names of `tokens.css` without the prefix.

### Ground and lines

| Token | Dark | Light | Use |
|---|---|---|---|
| `bg` | `#0F1418` | `#FAFBFB` | The page; the gaps between panes |
| `bg-raised` | `#161C21` | `#FFFFFF` | Bars, pane headers, cards |
| `bg-sunken` | `#0A0E11` | `#F1F3F4` | Terminals, code, text fields, the rail |
| `bg-overlay` | `#1B2329` | `#FFFFFF` | Dialogs, menus, toasts, tooltips |
| `bg-hover` | `#1D252B` | `#EEF1F2` | A row or quiet button under the pointer |
| `bg-active` | `#252F36` | `#E3E7E9` | Pressed; the chosen neutral item |
| `line-subtle` | `#1F272D` | `#E3E7E9` | Between related things. Decorative, under 3:1 on purpose |
| `line` | `#2C363D` | `#CBD2D6` | Container edges. Decorative |
| `line-strong` | `#6A7982` | `#7A868C` | Anything operable: fields, outline buttons. Dark 4.12 / 3.82 / 4.31 / 3.54 :1 on bg / raised / sunken / overlay; light 3.61:1 on bg |

`line` and `line-subtle` separate; they do not delimit a control. Anything a
user can operate gets `line-strong`, which meets WCAG 1.4.11.

### Text

| Token | Dark | Light | Ratios (on bg / raised / sunken / overlay) |
|---|---|---|---|
| `fg` | `#E6EDF1` | `#131A1E` | Dark 15.66 / 14.52 / 16.38 / 13.46; light 16.96 / 17.59 / 15.80 / 17.59 |
| `fg-muted` | `#93A1A9` | `#5C676D` | Dark 6.98 / 6.47 / 7.30 / 6.00; light 5.60 / 5.81 / 5.22 / 5.81 |
| `fg-faint` | `#738087` | `#7A868C` | Glyphs and rules only, never words |

Placeholders use `fg-muted`, not `fg-faint`.

### Accent: the mark's cyan

| Token | Dark | Light | Notes |
|---|---|---|---|
| `accent` | `#4FD1DB` | `#0B6E77` | 10.12 / 5.77 :1 on bg. Focus ring, links, primary button, focused pane, chosen-row bar |
| `accent-strong` | `#7BE0E8` | `#08575E` | Hover and pressed. Brightens in dark, deepens in light |
| `accent-subtle` | `#0E2B2E` | `#E6F3F4` | The chosen row; a tinted fill. `fg` and `fg-muted` both pass on it |
| `accent-line` | `#1F4E53` | `#A9D5D8` | Edge of a tinted fill; link underline |
| `on-accent` | `#0F1418` | `#FFFFFF` | 10.12 / 5.98 :1 on the accent fill |

### Status: only ever set from a state the app knows

| State | Mark | Token | Dark | Light | Subtle fill / line (dark) | Subtle fill / line (light) |
|---|---|---|---|---|---|---|
| Working | filled circle, breathing | `working` | `#4FD1DB` | `#0B6E77` | `#0E2B2E` / `#1F4E53` | `#E6F3F4` / `#A9D5D8` |
| Waiting on you, Blocked | filled triangle | `waiting` | `#E2B341` | `#875A08` | `#2A2110` / `#4E3D18` | `#FAF0DC` / `#E3CE96` |
| Idle | hollow circle | `idle` | `#93A1A9` | `#5C676D` | none | none |
| Starting | hollow circle, breathing | `idle` | same | same | none | none |
| Failed (error) | filled square | `failed` | `#F0776A` | `#B02A1F` | `#2C1512` / `#58251F` | `#FBEAE8` / `#EBB8B1` |
| Exited | hollow square | `exited` | `#738087` | `#7A868C` | none | none |

Two more semantic colours, neither of them a pane state:

| Meaning | Token | Dark | Light | Use |
|---|---|---|---|---|
| A verdict: passed, clean, lines added, ahead | `ok` | `#64C97F` | `#196634` | Clean tree, added lines, CI pass. Never a live state |
| Broadcast is on | `cast` | `#C98BE0` | `#7E3AA0` | The broadcast toggle and label, with ink `on-cast` (`#170D1C` / `#FFFFFF`) |

Inks on filled status colours: `on-waiting` `#1F1602` / `#FFFFFF`, `on-failed`
`#1F0907` / `#FFFFFF`, `on-ok` `#06180C` / `#FFFFFF`. All pass 4.5:1.

**Why working is cyan and not green.** The icon already draws the working bird
in cyan; green is a verdict; and it is measured. Colour-vision simulation
(Machado 2009, severity 1.0; distance in OKLab x100, under 10 treated as "the
same colour"):

| Pair (dark theme) | Normal | Protan | Deutan | Tritan |
|---|---|---|---|---|
| working (cyan) / waiting (amber) | 21.4 | 18.6 | 19.1 | 23.5 |
| green working / amber waiting (rejected) | 15.4 | **6.4** | **8.9** | 20.7 |
| green working / cyan accent (rejected) | 12.1 | 12.3 | 11.7 | **5.6** |

Cyan against amber lies on the blue-yellow axis that red-green colour blindness
keeps. Green against amber collapses for about one man in twelve, and those are
the two states a user most needs to tell apart.

**What colour alone still cannot do, and what covers it.** The script reports
four pairs under the threshold, and each is carried by shape:

| Pair | Worst case | Carried by |
|---|---|---|
| working / idle, light theme | 1.7 (protan) | filled against hollow; working moves |
| waiting / failed, light theme | 0.6 (deutan) | triangle against square |
| working / idle, dark | 9.5 (deutan) | filled against hollow; working moves |
| failed / idle, dark | 9.5 (protan) | square against circle; filled against hollow |

So the rule is absolute: **a status is never a coloured dot alone.**

**Tinted pane headers.** A waiting (or blocked) header is
`color-mix(in srgb, <waiting> 14%, <bg-raised>)` with a 2px top rule in the
status colour (`box-shadow: inset 0 2px 0 <waiting>`). Failed is the same with
`failed`. At 14% every header text colour (`fg`, `fg-muted`, `accent`,
`waiting`, `failed`, `ok`, `cast`, `working`) passes 4.5:1 on both tints in
both themes with no per-header overrides; at 20% they do not.

### The terminal is dark in both themes

It is the content, and an agent's ANSI colours are chosen for a dark ground.

| Token | Value | Ratio on `term-bg` |
|---|---|---|
| `term-bg` | `#0A0E11` | |
| `term-fg` | `#D7DFE4` | 14.36:1 |
| `term-cursor` | `#4FD1DB` | 10.59:1 |
| `term-selection` | `#1F4E53` | `term-fg` on it: 6.85:1 |

`ansi-0` to `ansi-15` (in `tokens.css`): slots 1 to 15 all reach 4.5:1 on
`term-bg` (lowest: slot 8 at 4.77:1), and slot 6 is the accent, which is the
join between the app and the terminal it wraps. Agent output arrives with its
own SGR codes and is never restyled; this only decides what the 16 slots
resolve to, inside `bg-sunken`. **Inside that surface the agent's colours,
outside it ours.** The desktop app applies only background, foreground, cursor
and selection in the first restyle; the 16 colours are their own change.

### Accent swatches in the desktop app

Settings > Appearance. A swatch changes focus, links, the primary fill and the
chosen-row bar. It never changes a status.

| Swatch | Dark | Light |
|---|---|---|
| Cyan (default) | `#4FD1DB` | `#0B6E77` |
| Blue | `#6CB0FF` | `#1F5FA8` |
| Purple | `#B99AF5` | `#7044C4` |
| Green | `#64C97F` | `#196634` |
| Orange | `#F0A552` | `#9A4D08` |
| Pink | `#F28FC0` | `#A8327A` |

### Overlays and shadows

Scrim `--fd-scrim`, flat, no blur. Three shadows, tinted to the ramp, never
plain black on a light ground: `shadow-1` a raised chip over a terminal,
`shadow-2` toasts, tooltips and menus, `shadow-3` dialogs, the palette and a
screenshot on the site. In dark, separation comes from the 1px `line` edge
first; the shadow only lifts an overlay off the terminals. Cards have no shadow.

## The status marks

One element, drawn in CSS (`.fd-st` in `tokens.css`), sized in px so it does not
scale with text.

| State | Size | CSS |
|---|---|---|
| working | 8px | `border-radius: 50%; background: working; animation: pulse 1.6s` (opacity 1 to .4) |
| idle | 8px | `border-radius: 50%; box-shadow: inset 0 0 0 1.5px idle` |
| starting | 8px | idle, with the pulse |
| waiting, blocked | 10px | `background: waiting; clip-path: polygon(50% 6%, 100% 94%, 0 94%)` |
| failed | 8px | `border-radius: 1.5px; background: failed` |
| exited | 8px | `border-radius: 1.5px; box-shadow: inset 0 0 0 1.5px exited` |

Grammar: **circle = alive, triangle = needs you, square = ended; filled =
something is happening.** Working is the only mark that moves, and it stops
under `prefers-reduced-motion`. Under `forced-colors` the marks keep their
colours. Words, always the same: "Working", "Waiting on you", "Idle",
"Starting", "Failed", "Exited".

Where a glyph has to be text (a window title, a terminal, a push notification)
the characters stay `●` and `▲`. Inside a page, the mark is drawn.

## Type

| Role | Family | Licence | Notes |
|---|---|---|---|
| Sans: all words | **IBM Plex Sans**, variable, weight 100 to 700 | SIL OFL 1.1 | `--fd-sans` |
| Mono: all data | **JetBrains Mono**, variable, weight 100 to 800 | SIL OFL 1.1 | `--fd-mono`. Always `font-variant-ligatures: none` in UI, so `=>` and `!=` stay literal |

Self-hosted, one variable woff2 per family, no CDN, referenced as
`url("fonts/<file>")` in double quotes (`ibm-plex-sans.woff2`,
`jetbrains-mono.woff2`); no request leaves the origin. The JetBrains Mono
subset must contain the glyphs the product types: Basic Latin, Latin-1,
General Punctuation, Arrows U+2190-21FF, U+2387, Geometric Shapes U+25A0-25FF
and U+2713. Licence texts: [`fonts/OFL-IBMPlexSans.txt`](./fonts/OFL-IBMPlexSans.txt)
and [`fonts/OFL-JetBrainsMono.txt`](./fonts/OFL-JetBrainsMono.txt), kept beside
every shipped font file and reproduced in `THIRD-PARTY-NOTICES.md`. The
terminal's own font is the user's setting and is not touched.

**Scale.** Whole pixels only: `fs-10` a count in a badge; `fs-11` pane-header
metadata (mono); `fs-12` secondary text; `fs-13` the app's body; `fs-14` dialog
titles, small text on the site; `fs-16` body on the site, docs and phone;
`fs-18` lede; `fs-20`, `fs-24`, `fs-30`, `fs-38` headings; `fs-48`, `fs-60` the
site hero only.

**Weights.** 400 body, 500 labels and mono emphasis, 600 names, titles,
headings and buttons. 700 is not used. **Line height.** 1.08 display, 1.2
headings, 1.4 in the app and on controls, 1.65 prose, 1.6 code. **Tracking.**
Display -0.028em, h1 -0.022em, h2 -0.02em, h3 and card titles -0.01em, body 0.

**Where mono is used** (the data layer), on every surface: pane-header
metadata (branch, agent and model, tokens, cost, limits, CPU and memory, git
counts; the pane name stays sans 600); tallies and counts, always with
`tabular-nums`; keys, commands, paths, commit ids, branch names, fingerprints;
status words inside a pill, and tags; code and terminal excerpts. Not mono:
labels, buttons, headings, prose, menu items, tab titles.

## Space, shape, focus, motion

**Space.** A 4px grid, with 2 and 6 for the dense app: `sp-1` to `sp-14`
(2 4 6 8 12 16 20 24 32 40 48 64 96 128). No other values.

**Shape.** Three radii and a pill. `r-1` 4px: tags, keys, checkboxes, inline
code, a tab's close button. `r-2` 6px: everything operable (buttons, fields,
tabs, rail items, a pane, a toast). `r-3` 10px: containers (dialogs, the
palette, cards, code blocks, figures). `r-full`: status pills, switches, count
badges; nothing else. No pill buttons, no pill kickers.

**Focus.** Everything focusable: `outline: 2px solid <accent>; outline-offset:
2px` on `:focus-visible` (1px in the desktop app, whose chrome is tight). Text
fields: the edge becomes the accent and doubles, `border-color: <accent>;
box-shadow: 0 0 0 1px <accent>`, no glow. A divider or resize handle lights its
own line in the accent. `forced-colors`: `outline: 2px solid Highlight
!important`.

**Motion.** `dur-1` 80ms linear for colour under the pointer; `dur-2` 140ms
ease-out for a press, a switch, the rail's width; `dur-3` 220ms ease-out for a
dialog, toast or sheet arriving (leaving: 160ms, ease-in, opacity only);
`dur-4` 400ms the site's one hero entrance; `pulse` 1.6s for the working and
starting marks, one speed everywhere. List the properties in every
`transition` (never `all`). A press is `translateY(1px)`, not a scale. Nothing
lifts on hover. The `prefers-reduced-motion` sweep stays in every stylesheet,
last before the `forced-colors` block.

## Token mapping

Keep each surface's variable names. Tests pin several of them, and renaming
buys nothing. The **values** must match, and `design.AssertSurface` proves it.
Beware the naming trap: on every surface the variable called `--exited` is the
red that this document calls `failed`. Keep the name, give it the `failed`
value, and use the new `--ended` for the exited mark.

| Canonical | Desktop `app.css` | Site `site.css`, docs `docs.css`, remote `app.css` |
|---|---|---|
| `bg` | `--bg` | `--surface` |
| `bg-raised` | `--bg-raised` | `--raised` |
| `bg-sunken` | `--bg-inset` | `--sunken` |
| `bg-overlay` | `--bg-overlay` (new) | `--overlay` (new) |
| `bg-hover` | `--bg-hover` (new) | `--hover` (new) |
| `bg-active` | `--bg-active` (new) | `--active` (new) |
| `line-subtle` | `--line-subtle` (new) | `--edge-soft` |
| `line` | `--line` | `--edge` |
| `line-strong` | `--line-strong` and `--field-line` | `--edge-hard` |
| `fg` | `--fg` | `--text` |
| `fg-muted` | `--fg-dim` | `--muted` |
| `fg-faint` | `--fg-faint` (new) | `--faint` (new) |
| `accent` family | `--accent`, `--accent-strong`, `--accent-subtle`, `--accent-line`, `--on-accent` | `--accent`, `--accent-strong`, `--accent-subtle`, `--accent-edge`, `--on-accent` |
| `working` | `--working` | `--working` |
| `waiting` | `--waiting` | `--waiting`, `--waiting-subtle`, `--waiting-edge` |
| `failed` | **`--exited`** | **`--exited`**, `--exited-subtle`, `--exited-edge` |
| `idle` | `--idle` | `--idle` |
| `exited` | `--ended` (new) | `--ended` (new) |
| `ok` | `--ok` (new) | `--ok` (new) |
| `cast` | `--cast`, `--on-cast` (new) | n/a |
| radii | `--radius` 6px, `--radius-sm` 4px, `--radius-lg` 10px | `--radius` 10px, `--radius-sm` 6px, plus 4px |
| fonts | `--sans`, `--mono` | `--body`, `--display` (both the sans), `--mono` |

## The state map

The app's own states map to tokens in one place, so a new state is a decision
made once rather than a colour picked at a call site:

```
working → accent    waiting, blocked → waiting    failed → failed
idle, starting → idle    exited → exited    passed, clean → ok
```

## Verification

```
node design/check-contrast.mjs                 # the report; exits 1 on a failure
node design/check-contrast.mjs --check-report  # the CI form
go test ./design
```
