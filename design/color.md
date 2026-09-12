# Color

The app's own status palette is the honest source. Green, amber and red are
spent on states the app actually knows; everything else is neutral, and the one
accent is a deepened terminal cyan — `#0B6E77` light, `#4FD1DB` dark — which
keeps us inside the ANSI world the app already lives in.

Tokens: [`color.css`](./color.css).

## Principles

**1. Status color is earned, never decorative.**
Green, amber and red appear only where the app is reporting a state it actually
knows. Not on a heading, not on a chart series, not on a marketing panel, not to
make a button feel positive. If you want a color and there is no state behind
it, the answer is the accent or a neutral.

**2. Running is accent, not green.**
Green is a verdict. A run in progress has not reached one. Activity — streaming,
thinking, connected — takes the accent cyan; only a finished, successful run
turns green. This is why the accent had to be cyan and not a brand color chosen
for taste: it has to sit next to the status trio without being read as one.
The pane dot still uses green for working; migrating it is outstanding.

**3. Never color alone.**
Every status carries a glyph and a word. The color is the third channel, not
the first. This is not a general nicety — it is load-bearing here, see below.

**4. One accent, no second brand hue.**
Two accents and the eye starts assigning meaning to the difference. There is one
accent; emphasis comes from `--c-accent-strong`, weight and space.

**5. Neutrals carry the UI.**
Nearly every surface, border and glyph is neutral. Color is the exception, which
is what makes it legible when it appears.

## Palette

Every value below is verified at its intended pairing; ratios are WCAG 2.1
contrast against the surface it sits on.

### Neutral

| Token | Light | Dark | Use |
|---|---|---|---|
| `--c-surface` | `#FAFBFB` | `#0F1418` | The page |
| `--c-raised` | `#FFFFFF` | `#161C21` | Cards, panels, popovers |
| `--c-sunken` | `#F1F3F4` | `#0A0E11` | Agent output, code, terminal blocks |
| `--c-border-subtle` | `#E3E7E9` | `#1F272D` | Separators between related things |
| `--c-border` | `#CBD2D6` | `#2C363D` | Container edges |
| `--c-border-strong` | `#7A868C` | `#5D6B72` | Control boundaries — 3.61:1 / 3.36:1 |
| `--c-text` | `#131A1E` | `#E6EDF1` | 16.96:1 / 15.66:1 |
| `--c-text-muted` | `#5C676D` | `#93A1A9` | 5.60:1 / 6.98:1 |

`--c-border` and `--c-border-subtle` are decorative and deliberately below 3:1 —
they separate, they don't delimit a control. Anything a user can operate gets
`--c-border-strong`, which meets WCAG 1.4.11.

### Accent

| Token | Light | Dark | Notes |
|---|---|---|---|
| `--c-accent` | `#0B6E77` | `#4FD1DB` | 5.77:1 / 10.12:1 |
| `--c-accent-strong` | `#08575E` | `#7BE0E8` | Hover, pressed. Deepens in light, brightens in dark |
| `--c-accent-subtle` | `#E6F3F4` | `#0E2B2E` | Tinted fill; accent text on it holds 5.27:1 / 8.18:1 |
| `--c-accent-border` | `#A9D5D8` | `#1F4E53` | |
| `--c-on-accent` | `#FFFFFF` | `#0F1418` | On an accent fill: 5.98:1 / 10.12:1 |

### Status

| State | Token | Light | Dark |
|---|---|---|---|
| done | `--c-ok` | `#196634` (6.77:1) | `#64C97F` (9.00:1) |
| blocked / needs input | `--c-warn` | `#92610A` (5.15:1) | `#E2B341` (9.49:1) |
| failed | `--c-err` | `#B02A1F` (6.34:1) | `#F0776A` (6.66:1) |

Each has a `-subtle` fill and a `-border`; status text on its own subtle fill
stays at or above 4.5:1 in both themes (lowest pairing is light amber on
`--c-warn-subtle`, 4.72:1).

Idle, queued and cancelled are `--c-text-muted`. They are not states worth
spending a hue on, and cancelled in particular must not read as failure.

### The state map

`color.css` maps app states to tokens in one place, so a new state is a decision
made once rather than a color picked at a call site:

```
idle → muted    queued → muted     running → accent
done → ok       blocked → warn     failed → err      cancelled → muted
```

## Why rule 3 is load-bearing

Simulating deuteranopia (the most common color vision deficiency, ~6% of men)
on our own palette:

| Pair | Normal vision | As a deuteranope sees it |
|---|---|---|
| dark accent vs. dark green | distinct | **1.25:1 — effectively the same color** |
| light accent vs. light green | distinct | 2.75:1 |

Cyan and green collapse into each other in dark mode. We tested the alternatives:
shifting green yellow-ward gets it to 1.47:1, which is still indistinguishable.
There is no hue pair that solves this at these lightnesses while keeping the
brief's accent.

That matters more here than in most apps, because *running* and *done* are
adjacent states on the same row — the two a user most needs to tell apart at a
glance. So the separation cannot live in the color:

- every status carries a **glyph** (running is the only animated one) and a **label**;
- running is the only state that moves, which is a channel color can't fake;
- never encode a state in a colored dot alone, in either theme.

The palette choice is sound; the redundancy is what makes it safe.

## ANSI passthrough

Agent output arrives with its own SGR escape codes. We don't restyle it — that
would misrepresent what the wrapped agent said. We only decide what the 16 slots
resolve to, inside `--c-sunken`. That surface is the boundary: **inside it the
agent's colors, outside it ours.**

Two consequences worth naming:

- In light mode the *bright* half of the palette **deepens** rather than lightens
  (`bright green` is `#114B26`, darker than `green` `#196634`). Bright means
  emphasis, and on a light ground emphasis is more contrast, not less.
- `ANSI black` in dark mode is a fill color, not a foreground one — at 1.49:1 it
  is unreadable as text. The renderer lifts foreground `ANSI black` to
  `--ansi-8` rather than emitting invisible output. Nothing else in the ANSI set
  needs a lift; all 15 other slots clear 4.5:1 on `--c-sunken` in both themes.

`--ansi-6` / cyan is the same value as `--c-accent`, in both themes. That is the
join: the app's accent is a color the terminal already had.

## Verification

`design/contrast.js` checks every pairing this document claims, and exits
non-zero if one regresses. Run it before changing a value:

```
node design/contrast.js
```
