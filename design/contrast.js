#!/usr/bin/env node
/* Verifies every contrast pairing design/color.md claims.
 * Values are parsed out of color.css, so the check can't drift from what ships.
 * Exits non-zero on any regression. */

const fs = require('fs');
const path = require('path');

const css = fs.readFileSync(path.join(__dirname, 'color.css'), 'utf8');

/* Light lives on bare `:root {`; dark on `:root[data-theme="dark"] {`.
 * Read those two blocks; dark inherits anything it doesn't override. */
const block = (startRe) => {
  const m = startRe.exec(css);
  if (!m) throw new Error(`could not find block ${startRe}`);
  const body = css.slice(m.index + m[0].length);
  const end = body.indexOf('\n}');
  const out = {};
  for (const [, k, v] of body.slice(0, end).matchAll(/(--[\w-]+):\s*(#[0-9A-Fa-f]{6})/g)) out[k] = v;
  return out;
};

const light = block(/:root\s*\{/);
const dark = { ...light, ...block(/:root\[data-theme="dark"\]\s*\{/) };

const lin = c => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4));
const lum = hex => {
  const h = hex.replace('#', '');
  const [r, g, b] = [0, 2, 4].map(i => parseInt(h.slice(i, i + 2), 16) / 255).map(lin);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const ratio = (a, b) => {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
};

let failed = 0;
const check = (theme, tokens, fg, bg, min, note = '') => {
  const [f, b] = [tokens[fg] ?? fg, tokens[bg] ?? bg];
  if (!f || !b) { console.log(`  MISSING ${fg} / ${bg}`); failed++; return; }
  const v = ratio(f, b);
  const ok = v >= min;
  if (!ok) failed++;
  console.log(
    `  ${ok ? 'ok  ' : 'FAIL'} ${theme} ${(fg + ' on ' + bg).padEnd(42)} ` +
    `${v.toFixed(2).padStart(6)}:1 (min ${min})${note && '  ' + note}`
  );
};

/* Text and UI minimums: 4.5:1 for text (WCAG 1.4.3),
 * 3:1 for control boundaries (1.4.11). */
const suite = (theme, t) => {
  console.log(`\n${theme}`);
  for (const s of ['--c-surface', '--c-raised']) {
    check(theme, t, '--c-text', s, 4.5);
    check(theme, t, '--c-text-muted', s, 4.5);
    check(theme, t, '--c-accent', s, 4.5);
    check(theme, t, '--c-accent-strong', s, 4.5);
    check(theme, t, '--c-ok', s, 4.5);
    check(theme, t, '--c-warn', s, 4.5);
    check(theme, t, '--c-err', s, 4.5);
  }
  check(theme, t, '--c-border-strong', '--c-surface', 3, 'control boundary');
  check(theme, t, '--c-on-accent', '--c-accent', 4.5, 'text on accent fill');
  check(theme, t, '--c-accent', '--c-accent-subtle', 4.5);
  check(theme, t, '--c-ok', '--c-ok-subtle', 4.5);
  check(theme, t, '--c-warn', '--c-warn-subtle', 4.5);
  check(theme, t, '--c-err', '--c-err-subtle', 4.5);
  check(theme, t, '--c-focus', '--c-surface', 3, 'focus ring');

  /* ANSI, on the sunken surface it renders into.
   * Slot 0 is exempt: it is a fill color, and foreground ANSI black is
   * lifted to slot 8 by the renderer (see color.md, ANSI passthrough). */
  for (let i = 1; i <= 15; i++) check(theme, t, `--ansi-${i}`, '--c-sunken', 4.5);
};

suite('light', light);
suite('dark ', dark);

/* The accent must not be confusable with the status trio by luminance alone
 * at a glance — but cyan/green genuinely collapse under deuteranopia, which
 * is why status is never encoded in color alone. Asserted, not assumed: */
console.log('\ninvariants');
const inv = (label, cond) => { if (!cond) failed++; console.log(`  ${cond ? 'ok  ' : 'FAIL'} ${label}`); };
inv('accent === ANSI cyan (light)', light['--c-accent'] === light['--ansi-6']);
inv('accent === ANSI cyan (dark)', dark['--c-accent'] === dark['--ansi-6']);
inv('running is accent, not ok', /--state-running:\s*var\(--c-accent\)/.test(css));
inv('light bright-green is deeper than green', lum(light['--ansi-10']) < lum(light['--ansi-2']));
inv('dark bright-green is lighter than green', lum(dark['--ansi-10']) > lum(dark['--ansi-2']));

console.log(failed ? `\n${failed} failing check(s)` : '\nall checks pass');
process.exit(failed ? 1 : 0);
