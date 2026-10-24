// The settings kept in the interface's own blob (prefs.ui): read from it, sent as
// one uiPrefs command when the desktop offers it, and settled by the desktop's push.
import { test } from "node:test";
import assert from "node:assert/strict";
import { FONT_DEFAULT, SCROLLBACK_DEFAULT, UI_CAP, UI_KEYS, addPending, latestOf, prefsOf, settledPending, setting } from "../src/lib/prefs.js";

const CAPS = [UI_CAP];

test("the four settings are read from the blob, and from the old field where the blob has none", () => {
  const a = prefsOf({ ui: { fontSize: 18, fontFamily: "Fira Code", scrollback: 8000, helpSeen: true } });
  assert.deepEqual([a.fontSize, a.fontFamily, a.scrollback, a.helpSeen], [18, "Fira Code", 8000, true]);
  const b = prefsOf({ fontSize: 15, helpSeen: true });
  assert.deepEqual([b.fontSize, b.helpSeen], [15, true], "an older desktop");
  const c = prefsOf({ fontSize: 15, ui: { fontSize: 20 } });
  assert.equal(c.fontSize, 20, "the blob wins");
  assert.equal(prefsOf({ ui: { fontSize: "big", scrollback: -1 } }).fontSize, FONT_DEFAULT);
  assert.equal(prefsOf({ ui: { scrollback: -1 } }).scrollback, SCROLLBACK_DEFAULT);
  for (const bad of [null, [], "x", 3]) assert.equal(prefsOf({ ui: bad }).fontSize, FONT_DEFAULT, JSON.stringify(bad));
  assert.deepEqual(UI_KEYS, ["fontSize", "fontFamily", "scrollback", "helpSeen"]);
});

test("with the feature each of the four is one uiPrefs patch, and the shown value is the whole blob in the desktop's key order", () => {
  const cur = { ui: { scrollback: 5000, zeta: 1 } };
  const f = setting("fontSize", 16, cur, CAPS);
  assert.deepEqual(f.command, { cmd: "uiPrefs", ui: { fontSize: 16 } });
  assert.deepEqual(f.patch, { ui: { fontSize: 16, scrollback: 5000, zeta: 1 } });
  assert.deepEqual(Object.keys(f.patch.ui), ["fontSize", "scrollback", "zeta"], "sorted, as the desktop sends a map");
  assert.deepEqual(setting("scrollback", 9000, cur, CAPS).command, { cmd: "uiPrefs", ui: { scrollback: 9000 } });
  assert.deepEqual(setting("helpSeen", true, cur, CAPS).command, { cmd: "uiPrefs", ui: { helpSeen: true } });
  const fam = setting("fontFamily", "  Fira Code ", cur, CAPS);
  assert.deepEqual(fam.command, { cmd: "uiPrefs", ui: { fontFamily: "Fira Code" } });
  const none = setting("fontFamily", "  ", { ui: { fontFamily: "x", a: 1 } }, CAPS);
  assert.deepEqual(none.command, { cmd: "uiPrefs", ui: { fontFamily: null } }, "the default removes the key");
  assert.deepEqual(none.patch, { ui: { a: 1 } });
});

test("a value the desktop would refuse is still not sent", () => {
  assert.equal(setting("fontSize", 99, {}, CAPS), null);
  assert.equal(setting("scrollback", 5, {}, CAPS), null);
  assert.equal(setting("fontFamily", "x".repeat(500), {}, CAPS), null);
});

test("without the feature the old commands are used, as before", () => {
  assert.deepEqual(setting("fontSize", 16, {}, []).command, { cmd: "fontSize", size: 16 });
  assert.deepEqual(setting("fontSize", 16, {}).command, { cmd: "fontSize", size: 16 });
  assert.deepEqual(setting("helpSeen", true, {}, null).command, { cmd: "helpSeen" });
  assert.deepEqual(setting("fontFamily", "A", {}, ["clientHello"]).command, { cmd: "fontFamily", text: "A" });
  assert.deepEqual(setting("scrollback", 3000, {}, []).patch, { scrollback: 3000 });
  // A setting that is not in the blob is not changed by having the feature.
  assert.deepEqual(setting("railWidth", 200, {}, CAPS).command, { cmd: "railWidth", size: 200 });
  assert.deepEqual(setting("dismissedTips", 1, {}, CAPS), null);
});

test("the desktop's push settles what was sent, and a change by another window ends the wait", () => {
  let pending = {};
  const first = setting("fontSize", 16, { ui: {} }, CAPS);
  pending = addPending(pending, first.patch);
  const second = setting("scrollback", 7000, { ui: { fontSize: 16 } }, CAPS);
  pending = addPending(pending, second.patch);
  assert.deepEqual(latestOf(pending).ui, { fontSize: 16, scrollback: 7000 });
  // The answer to the first command arrives: the second still waits.
  pending = settledPending(pending, { ui: { fontSize: 16 } }, {});
  assert.deepEqual(pending.ui, [{ fontSize: 16, scrollback: 7000 }]);
  // The answer to the second settles it.
  pending = settledPending(pending, { ui: { fontSize: 16, scrollback: 7000 } }, { ui: { fontSize: 16 } });
  assert.deepEqual(pending, {});
});
