#!/usr/bin/env node
/* Flockdeck design tokens: contrast and colour-vision checks.
 *
 *   node design/check-contrast.mjs            prints the report, exits 1 on any failure
 *   node design/check-contrast.mjs --md       prints it as markdown (design/contrast-report.md)
 *   node design/check-contrast.mjs --check-report
 *                                             the CI form: runs every check, and also fails if
 *                                             contrast-report.md is stale; prints only failures
 *
 * The palette is read from tokens.css in this folder, so the check cannot drift
 * from what ships: change a value in tokens.css, rerun, and only then change a
 * stylesheet. Contrast is WCAG 2.1 relative-luminance contrast. Colour-vision
 * simulation is Machado, Oliveira & Fernandes (2009) at severity 1.0, and the
 * distance between two simulated colours is Euclidean distance in OKLab (x100).
 * Run from any directory; needs only node. */

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, 'tokens.css'), 'utf8');

/* The text between the braces of the first block that opens with `header`. */
const blockOf = header => {
  const at = css.indexOf(header);
  if (at < 0) throw new Error(`tokens.css: no block ${header}`);
  const open = css.indexOf('{', at);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}' && --depth === 0) return css.slice(open + 1, i);
  }
  throw new Error(`tokens.css: unterminated block ${header}`);
};
const declared = body => {
  const out = {};
  for (const [, k, v] of body.matchAll(/--fd-([\w-]+):\s*(#[0-9A-Fa-f]{6})\s*;/g)) out[k] = v.toUpperCase();
  return out;
};

const darkRaw = declared(blockOf(':root {'));
const lightRaw = declared(blockOf(':root[data-theme="light"]'));
const systemRaw = declared(blockOf(':root[data-theme="system"]'));

/* The system block must say exactly what the light block says. */
const drift = [];
for (const k of new Set([...Object.keys(lightRaw), ...Object.keys(systemRaw)])) {
  if (lightRaw[k] !== systemRaw[k]) drift.push(`--fd-${k}: light ${lightRaw[k]} vs system ${systemRaw[k]}`);
}
if (drift.length) { console.error('tokens.css: the light and system blocks differ:\n  ' + drift.join('\n  ')); process.exit(1); }

/* --fd-bg-raised -> raised, --fd-line-subtle -> lineSubtle, --fd-on-accent -> onAccent. */
const camel = k => k.replace(/^bg-(?=.)/, '').replace(/-(\w)/g, (_, c) => c.toUpperCase());
const theme = raw => {
  const t = {};
  for (const [k, v] of Object.entries(raw)) if (!/^(term|ansi|swatch)-/.test(k)) t[camel(k)] = v;
  return t;
};
const missing = Object.keys(theme(darkRaw)).filter(k => !(k in theme(lightRaw)));
if (missing.length) { console.error(`tokens.css: the light theme does not redefine: ${missing.join(', ')}`); process.exit(1); }

export const dark = theme(darkRaw);
export const light = { ...dark, ...theme(lightRaw) };

/* The terminal is dark in both themes: it is the content, and an agent's own
 * ANSI colours are chosen for a dark ground. */
export const term = {
  bg: darkRaw['term-bg'], fg: darkRaw['term-fg'], cursor: darkRaw['term-cursor'], selection: darkRaw['term-selection'],
  ansi: Array.from({ length: 16 }, (_, i) => darkRaw[`ansi-${i}`]),
};
if (term.ansi.includes(undefined) || !term.bg) { console.error('tokens.css: term-* or ansi-0..15 missing'); process.exit(1); }

/* The accent swatches Settings > Appearance offers in the desktop app. */
const swatchesOf = raw => Object.fromEntries(Object.entries(raw).filter(([k]) => k.startsWith('swatch-')).map(([k, v]) => [k.slice(7), v]));
export const swatches = { dark: swatchesOf(darkRaw), light: swatchesOf(lightRaw) };

// ---------------------------------------------------------------- maths

const rgb = h => [1, 3, 5].map(i => parseInt(h.slice(i, i + 2), 16));
const hex = c => '#' + c.map(v => Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, '0')).join('').toUpperCase();
const lin = v => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
const unlin = v => 255 * (v <= 0.0031308 ? 12.92 * v : 1.055 * Math.max(v, 0) ** (1 / 2.4) - 0.055);
const lum = h => { const [r, g, b] = rgb(h).map(lin); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
export const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };
/* color-mix(in srgb, a p%, b): what a tinted pane header comes to. */
export const mix = (a, p, b) => hex(rgb(a).map((v, i) => v * p / 100 + rgb(b)[i] * (100 - p) / 100));

const CVD = {
  protan: [[0.152286, 1.052583, -0.204868], [0.114503, 0.786281, 0.099216], [-0.003882, -0.048116, 1.051998]],
  deutan: [[0.367322, 0.860646, -0.227968], [0.280085, 0.672501, 0.047413], [-0.011820, 0.042940, 0.968881]],
  tritan: [[1.255528, -0.076749, -0.178779], [-0.078411, 0.930809, 0.147602], [0.004733, 0.691367, 0.303900]],
};
const simulate = (h, kind) => {
  if (kind === 'normal') return h;
  const l = rgb(h).map(lin), m = CVD[kind];
  return hex(m.map(row => unlin(row[0] * l[0] + row[1] * l[1] + row[2] * l[2])));
};
const oklab = h => {
  const [r, g, b] = rgb(h).map(lin);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
          1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
          0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s];
};
const dE = (a, b) => { const [p, q] = [oklab(a), oklab(b)]; return 100 * Math.hypot(p[0] - q[0], p[1] - q[1], p[2] - q[2]); };

// ---------------------------------------------------------------- checks

const reportCheck = process.argv.includes('--check-report');
const md = process.argv.includes('--md') || reportCheck;
let failed = 0;
const rows = [];
const check = (theme, what, fg, bg, min) => {
  const r = ratio(fg, bg), ok = r >= min;
  if (!ok) failed++;
  rows.push({ theme, what, fg, bg, r, min, ok });
};

for (const [name, t] of [['dark', dark], ['light', light]]) {
  for (const [g, gv] of [['bg', t.bg], ['raised', t.raised], ['sunken', t.sunken], ['overlay', t.overlay], ['hover', t.hover], ['active', t.active]]) {
    check(name, `fg on ${g}`, t.fg, gv, 4.5);
    check(name, `fg-muted on ${g}`, t.fgMuted, gv, 4.5);
    check(name, `accent on ${g}`, t.accent, gv, 4.5);
    check(name, `waiting on ${g}`, t.waiting, gv, 4.5);
    check(name, `failed on ${g}`, t.failed, gv, 4.5);
    check(name, `ok on ${g}`, t.ok, gv, 4.5);
    check(name, `cast on ${g}`, t.cast, gv, 4.5);
    check(name, `line-strong on ${g} (control edge)`, t.lineStrong, gv, 3);
    check(name, `exited glyph on ${g} (non-text)`, t.exited, gv, 3);
  }
  check(name, 'fg-faint on bg (non-text, placeholders use fg-muted)', t.fgFaint, t.bg, 3);
  check(name, 'on-accent on accent (primary button)', t.onAccent, t.accent, 4.5);
  check(name, 'on-accent on accent-strong (primary hover)', t.onAccent, t.accentStrong, 4.5);
  check(name, 'on-failed on failed (danger button, filled)', t.onFailed, t.failed, 4.5);
  check(name, 'on-waiting on waiting (filled)', t.onWaiting, t.waiting, 4.5);
  check(name, 'on-ok on ok (filled)', t.onOk, t.ok, 4.5);
  check(name, 'on-cast on cast (broadcast on)', t.onCast, t.cast, 4.5);
  check(name, 'accent on accent-subtle (selected row, link on tint)', t.accent, t.accentSubtle, 4.5);
  check(name, 'fg on accent-subtle (selected row label)', t.fg, t.accentSubtle, 4.5);
  check(name, 'fg-muted on accent-subtle (selected row hint)', t.fgMuted, t.accentSubtle, 4.5);
  check(name, 'waiting on waiting-subtle (pill)', t.waiting, t.waitingSubtle, 4.5);
  check(name, 'failed on failed-subtle (pill)', t.failed, t.failedSubtle, 4.5);
  check(name, 'ok on ok-subtle (pill)', t.ok, t.okSubtle, 4.5);
  check(name, 'working on working-subtle (pill)', t.working, t.workingSubtle, 4.5);
  check(name, 'cast on cast-subtle (pill)', t.cast, t.castSubtle, 4.5);
  check(name, 'focus ring (accent) on bg', t.accent, t.bg, 3);
  check(name, 'focus ring (accent) on raised', t.accent, t.raised, 3);
  check(name, 'focus ring (accent) on sunken', t.accent, t.sunken, 3);

  /* The tinted pane headers: color-mix(in srgb, status P%, raised). */
  const wait = mix(t.waiting, 14, t.raised), fail = mix(t.failed, 14, t.raised);
  for (const [g, gv] of [[`waiting header ${wait}`, wait], [`failed header ${fail}`, fail]]) {
    for (const k of ['fg', 'fgMuted', 'accent', 'waiting', 'failed', 'ok', 'cast', 'working']) check(name, `${k} on ${g}`, t[k], gv, 4.5);
  }
  for (const [k, v] of Object.entries(swatches[name])) {
    check(name, `swatch ${k} on raised`, v, t.raised, 4.5);
    check(name, `swatch ${k} on sunken`, v, t.sunken, 4.5);
    check(name, `on-accent on swatch ${k}`, t.onAccent, v, 4.5);
  }
}
check('term', 'terminal fg on terminal bg', term.fg, term.bg, 7);
check('term', 'cursor on terminal bg', term.cursor, term.bg, 3);
check('term', 'fg on selection', term.fg, term.selection, 4.5);
term.ansi.forEach((c, i) => { if (i) check('term', `ansi ${i} on terminal bg`, c, term.bg, 4.5); });
/* A focused pane's border, and a status mark, in the light theme sit against the dark terminal too. */
check('light', 'accent (focused pane border) against the dark terminal', light.accent, term.bg, 3);

const out = [];
if (md) {
  out.push('| Theme | Pairing | Fg | Bg | Ratio | Needs |', '|---|---|---|---|---|---|');
  for (const r of rows) out.push(`| ${r.theme} | ${r.what} | \`${r.fg}\` | \`${r.bg}\` | ${r.ok ? '' : '**FAIL** '}${r.r.toFixed(2)}:1 | ${r.min}:1 |`);
} else {
  for (const r of rows) out.push(`${r.ok ? 'ok  ' : 'FAIL'} ${r.theme.padEnd(5)} ${r.what.padEnd(58)} ${r.fg} on ${r.bg} ${r.r.toFixed(2).padStart(6)}:1 (min ${r.min})`);
}

/* Status marks must be told apart without relying on hue: report how far apart
 * every pair of status colours is, for normal vision and the three dichromacies. */
const cvdRows = [];
const statusSet = t => ({ working: t.working, waiting: t.waiting, failed: t.failed, idle: t.idle, ok: t.ok, cast: t.cast });
const THRESH = 10;   // OKLab x100; under this a pair is treated as "same colour" and must differ by shape
for (const [name, t] of [['dark', dark], ['light', light]]) {
  const s = statusSet(t), keys = Object.keys(s);
  for (let i = 0; i < keys.length; i++) for (let j = i + 1; j < keys.length; j++) {
    const a = s[keys[i]], b = s[keys[j]];
    const d = ['normal', 'protan', 'deutan', 'tritan'].map(k => dE(simulate(a, k), simulate(b, k)));
    cvdRows.push({ theme: name, pair: `${keys[i]} / ${keys[j]}`, d, min: Math.min(...d) });
  }
}
/* The alternative this direction rejects: working drawn in green. */
const alt = ['normal', 'protan', 'deutan', 'tritan'].map(k => dE(simulate('#64C97F', k), simulate('#E2B341', k)));
const altAccent = ['normal', 'protan', 'deutan', 'tritan'].map(k => dE(simulate('#64C97F', k), simulate('#4FD1DB', k)));

out.push('');
if (md) {
  out.push('| Theme | Pair | Normal | Protan | Deutan | Tritan | Separated by |', '|---|---|---|---|---|---|---|');
  for (const r of cvdRows) out.push(`| ${r.theme} | ${r.pair} | ${r.d.map(v => v.toFixed(1)).join(' | ')} | ${r.min >= THRESH ? 'colour, shape' : '**shape only** (worst ' + r.min.toFixed(1) + ')'} |`);
  out.push('', `Rejected alternative, dark: green working \`#64C97F\` vs amber waiting: ${alt.map(v => v.toFixed(1)).join(' / ')} (normal / protan / deutan / tritan); green working vs cyan accent: ${altAccent.map(v => v.toFixed(1)).join(' / ')}.`);
} else {
  out.push('status pairs, OKLab distance x100 (normal / protan / deutan / tritan); under ' + THRESH + ' means shape must carry it');
  for (const r of cvdRows) out.push(`${r.min >= THRESH ? 'ok  ' : 'SHAPE'} ${r.theme.padEnd(5)} ${r.pair.padEnd(20)} ${r.d.map(v => v.toFixed(1).padStart(6)).join(' ')}`);
  out.push(`rejected: green working vs amber waiting  ${alt.map(v => v.toFixed(1).padStart(6)).join(' ')}`);
  out.push(`rejected: green working vs cyan accent    ${altAccent.map(v => v.toFixed(1).padStart(6)).join(' ')}`);
}
/* Structural invariants that no single ratio expresses. */
const fail = msg => { out.push(`FAIL ${msg}`); failed++; };
if (dark.accent !== swatches.dark.cyan || light.accent !== swatches.light.cyan) fail('the cyan swatch must equal the accent in both themes');
if (term.ansi[6] !== dark.accent) fail('ANSI 6 must equal the accent');
if (dark.working !== dark.accent || light.working !== light.accent) fail('working must equal the accent (owner decision 1)');
if (reportCheck) {
  const committed = readFileSync(join(here, 'contrast-report.md'), 'utf8').replace(/\r\n/g, '\n')
    .replace(/\n+(all contrast checks pass|\d+ failing contrast check\(s\))\s*$/, '');
  const stale = committed.trimEnd() !== out.join('\n').trimEnd();
  if (stale) fail('contrast-report.md is stale: node design/check-contrast.mjs --md > design/contrast-report.md');
}

console.log(reportCheck ? out.filter(l => l.startsWith('FAIL')).join('\n') : out.join('\n'));
console.log(failed ? `\n${failed} failing contrast check(s)` : '\nall contrast checks pass');
process.exit(failed ? 1 : 0);
