// L7: updates, versions, detach and quit, on a fake socket. The dialog, the
// commands it sends, what a window reached through the relay does not get, and
// release text staying text.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import { notesBlocks, webUrl, updateOf, quitQuestion, installLabel, versionsOf, RELAY_WORDS } from "../assets/lib/updates.js";
import { watchPageLifecycle } from "../assets/lib/page-lifecycle.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }];
const KEYS = [
  { id: "detach", keys: "Ctrl+Shift+Q", label: "Detach", section: "The window" },
  { id: "quit", keys: "Ctrl+Alt+Q", label: "Quit", section: "The window", confirm: "Stop every agent in every open project?" },
];
const UPDATE = { version: "v9.9.9", notes: "# Notes\n\n## Fixed\n\n- the **bold** thing\n- a `code` thing\n\nPlain words.", url: "https://github.com/x/y/releases/tag/v9.9.9" };
const stateMsg = (extra = {}) => ({ type: "state", root: "/work/a", projects, panes: {}, ...extra });

async function withPage(opts, fn) {
  const dom = createDom();
  const restore = install(dom);
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  render(html`<${Live} store=${store} window=${dom.window} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  const m = {
    dom, store, container, ws,
    sent: (c) => ws().commands(c),
    push: async (msg) => { ws().frame(msg); await settle(); },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    byText: (sel, re) => container.findAll(sel).find((e) => re.test(e.textContent)),
    key: async (key, init = {}) => {
      dom.window.dispatch("keydown", { key, ctrlKey: false, shiftKey: false, altKey: false, ...init });
      await settle();
    },
  };
  ws().open();
  // stateFirst: the state push arrives before the hello, so the hello is the
  // only thing that changes after the first render of the actions.
  if (opts.stateFirst) {
    ws().frame(stateMsg(opts.state));
    await settle();
  }
  ws().frame(hello({ keys: KEYS, prefs: opts.prefs || {}, remote: !!opts.remote }));
  await settle();
  if (!opts.stateFirst) {
    ws().frame(stateMsg(opts.state));
    await settle();
  }
  try {
    await fn(m);
  } finally {
    render(null, container);
    store.stop();
    restore();
  }
}

test("notes are shaped into headings, bullets and paragraphs, with bold and code kept as runs", () => {
  const b = notesBlocks(UPDATE.notes);
  assert.deepEqual(b.map((x) => x.kind), ["heading", "heading", "list", "para"]);
  assert.equal(b[0].level, 3);
  assert.equal(b[1].level, 4);
  assert.deepEqual(b[2].items[0].map((r) => r.t), ["text", "strong", "text"]);
  assert.deepEqual(b[2].items[1].map((r) => r.t), ["text", "code", "text"]);
});

test("http and https addresses are links", () => {
  assert.equal(webUrl("http://github.test/x"), "http://github.test/x");
  assert.equal(webUrl("https://github.test/x"), "https://github.test/x");
});

test("only http and https addresses are links", () => {
  assert.equal(webUrl("https://github.com/x"), "https://github.com/x");
  for (const bad of ["javascript:alert(1)", "data:text/html,x", "/relative", "", null, "ftp://x"]) assert.equal(webUrl(bad), "");
});

test("an update is read only when it has a version, and not in a remote window", () => {
  assert.equal(updateOf({ update: { notes: "x" } }, false), null);
  assert.equal(updateOf({ update: UPDATE }, true), null);
  assert.equal(updateOf({ update: UPDATE }, false).version, "v9.9.9");
  assert.equal(updateOf({ update: { version: 3 } }, false), null);
});

test("an update with an empty version is no update", () => {
  assert.equal(updateOf({ update: { version: "", notes: "x" } }, false), null);
});

test("a heading never goes past level 6", () => {
  assert.equal(notesBlocks("###### deep")[0].level, 6);
  assert.equal(notesBlocks("##### deep")[0].level, 6);
  assert.equal(notesBlocks("# top")[0].level, 3);
});

test("version rows are cleaned, and the install word follows the relation", () => {
  const v = versionsOf({ items: [{ version: "v1", relation: "older", notes: 4 }, { nope: 1 }, null] });
  assert.equal(v.items.length, 1);
  assert.equal(v.items[0].notes, "");
  assert.equal(installLabel("newer"), "Download update");
  assert.equal(installLabel("older"), "Download older version");
  assert.equal(installLabel("current"), "Download again");
  assert.equal(versionsOf(null), null);
});

test("the quit question is the key table's own, with a fallback", () => {
  assert.equal(quitQuestion([{ id: "quit", confirm: "Really?" }]), "Really?");
  assert.match(quitQuestion([]), /Stop every agent/);
});

test("the rail always has an Updates entry, and names the version when one is waiting", () =>
  withPage({}, async (m) => {
    assert.ok(m.byText(".rail-updates", /^\s*Updates\s*$/));
    await m.push(stateMsg({ update: UPDATE }));
    assert.ok(m.byText(".rail-updates", /Update v9\.9\.9/));
  }));

test("the update dialog shows notes as text, starts on Later and restarts only on request", () =>
  withPage({ state: { update: UPDATE } }, async (m) => {
    await m.click(m.byText(".rail-updates", /Update/));
    const d = m.container.find(".dialog");
    assert.match(d.textContent, /Update to v9\.9\.9/);
    assert.match(d.textContent, /agents running in panes are stopped/);
    assert.equal(d.find(".upd-notes").findAll("li").length, 2);
    assert.equal(d.find(".upd-notes").find("strong").textContent, "bold");
    assert.equal(d.find("a").attributes.href, "https://github.com/x/y/releases/tag/v9.9.9");
    assert.equal(m.dom.document.activeElement.textContent, "Later");
    await m.click(m.byText(".dialog-btn", /^Later$/));
    assert.equal(m.container.find(".dialog"), null);
    assert.equal(m.sent("restart").length, 0);
    await m.click(m.byText(".rail-updates", /Update/));
    await m.click(m.byText(".dialog-btn", /^Restart now$/));
    assert.equal(m.sent("restart").length, 1);
    assert.equal(m.container.find(".dialog"), null);
  }));

test("hostile release text is drawn as text and a hostile link is not a link", () =>
  withPage({ state: { update: { version: "<img src=x onerror=1>", notes: "<script>x</script> **<b>y</b>**", url: "javascript:alert(1)" } } }, async (m) => {
    await m.click(m.byText(".rail-updates", /Update/));
    const d = m.container.find(".dialog");
    assert.equal(d.find("script"), null);
    assert.equal(d.find("img"), null);
    assert.equal(d.find("b"), null);
    assert.equal(d.find("a"), null);
    assert.match(d.textContent, /<img src=x onerror=1>/);
    assert.match(d.textContent, /<script>x<\/script>/);
  }));

test("checking for an update sends the command, shows Checking until the answer, then the notice", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-updates", /Updates/));
    assert.match(m.container.find(".dialog").textContent, /No update is waiting/);
    await m.click(m.byText(".dialog-btn", /Check for updates now/));
    assert.equal(m.sent("checkForUpdate").length, 1);
    assert.ok(m.byText(".dialog-btn", /Checking/));
    await m.push({ type: "notice", text: "You are on the latest version." });
    assert.ok(m.byText(".dialog-btn", /Check for updates now/));
    assert.match(m.container.find(".dialog").textContent, /latest version/);
  }));

test("the switch sends updates off and on, and shows the preference", () =>
  withPage({ prefs: { updatesOff: true } }, async (m) => {
    await m.click(m.byText(".rail-updates", /Updates/));
    const box = m.container.find("input");
    assert.ok(!box.checked);
    box.checked = true;
    box.dispatch("change");
    await settle();
    assert.deepEqual(m.sent("updates")[0], { cmd: "updates", kind: "on" });
    box.checked = false;
    box.dispatch("change");
    await settle();
    assert.deepEqual(m.sent("updates")[1], { cmd: "updates", kind: "off" });
  }));

test("the versions list is asked for, drawn as text, and a click installs that version", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-updates", /Updates/));
    await m.click(m.byText(".dialog-btn", /Install a specific version/));
    assert.equal(m.sent("listVersions").length, 1);
    assert.match(m.container.find(".dialog").textContent, /Loading/);
    await m.push({ type: "versions", items: [
      { version: "v2.0.0", relation: "newer", published: "2026-10-01T10:00:00Z", notes: "First line\nsecond" },
      { version: "v1.0.0", relation: "current" },
      { version: "<b>v0</b>", relation: "older" },
    ] });
    const rows = m.container.findAll(".upd-version");
    assert.equal(rows.length, 3);
    assert.match(rows[0].textContent, /2026-10-01/);
    assert.match(rows[0].textContent, /Download update/);
    assert.match(rows[1].textContent, /running now/);
    assert.match(rows[1].textContent, /Download again/);
    assert.match(rows[2].textContent, /<b>v0<\/b>/);
    assert.equal(m.container.find(".upd-versions b"), null);
    await m.click(rows[2].find("button"));
    assert.deepEqual(m.sent("installVersion")[0], { cmd: "installVersion", text: "<b>v0</b>" });
    assert.match(m.container.find(".upd-versions").textContent, /Downloading/);
  }));

test("a failed list says why", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-updates", /Updates/));
    await m.click(m.byText(".dialog-btn", /Install a specific version/));
    await m.push({ type: "versions", items: [], error: "Could not reach GitHub." });
    assert.match(m.container.find(".dialog").textContent, /Could not reach GitHub/);
  }));

test("Escape closes the dialog and Tab stays inside it", () =>
  withPage({ state: { update: UPDATE } }, async (m) => {
    await m.click(m.byText(".rail-updates", /Update/));
    const d = m.container.find(".dialog");
    // Tab at the last control goes to the first, Shift+Tab at the first to the
    // last, and the browser is not left to move the keyboard out.
    const doc = m.dom.document;
    const input = d.find("input");
    const notes = d.find(".upd-notes");
    input.focus();
    let ev = input.dispatch("keydown", { key: "Tab", shiftKey: false });
    assert.ok(ev.defaultPrevented, "Tab was left to the browser");
    assert.equal(doc.activeElement, notes);
    ev = notes.dispatch("keydown", { key: "Tab", shiftKey: true });
    assert.ok(ev.defaultPrevented, "Shift+Tab was left to the browser");
    assert.equal(doc.activeElement, input);
    d.dispatch("keydown", { key: "Escape" });
    await settle();
    assert.equal(m.container.find(".dialog"), null);
  }));

test("the detach key sends detach, and a detached message closes the page", () =>
  withPage({}, async (m) => {
    let closed = 0;
    m.dom.window.close = () => { closed++; };
    await m.key("q", { ctrlKey: true, shiftKey: true });
    assert.equal(m.sent("detach").length, 1);
    await m.push({ type: "detached" });
    assert.equal(closed, 1);
    assert.match(m.container.find(".notice").textContent, /detached/);
  }));

test("quit asks first, with the key table's question, and sends nothing on Cancel", () =>
  withPage({}, async (m) => {
    await m.key("q", { ctrlKey: true, altKey: true });
    const c = m.container.find(".confirm");
    assert.ok(c);
    assert.match(c.textContent, /Stop every agent in every open project\?/);
    assert.equal(m.sent("quit").length, 0);
    assert.equal(m.dom.document.activeElement.textContent, "Cancel");
    // Tab walks Cancel, Quit and back; Shift+Tab walks the other way.
    const doc = m.dom.document;
    const label = () => doc.activeElement && doc.activeElement.textContent;
    const tab = async (shiftKey) => {
      const ev = doc.activeElement.dispatch("keydown", { key: "Tab", shiftKey });
      await settle();
      return ev;
    };
    assert.equal(label(), "Cancel");
    const ev = await tab(false);
    assert.ok(ev.defaultPrevented, "Tab was left to the browser");
    assert.equal(label(), "Quit and stop everything");
    await tab(false);
    assert.equal(label(), "Cancel");
    await tab(true);
    assert.equal(label(), "Quit and stop everything");
    await tab(true);
    assert.equal(label(), "Cancel");
    // Any other key is left alone: Enter and Space on Quit must not be taken.
    await tab(false);
    assert.equal(label(), "Quit and stop everything");
    for (const key of ["Enter", " ", "a"]) {
      const other = doc.activeElement.dispatch("keydown", { key, shiftKey: false });
      await settle();
      assert.ok(!other.defaultPrevented, JSON.stringify(key) + " was taken");
      assert.equal(label(), "Quit and stop everything");
    }
    await tab(false);
    assert.equal(label(), "Cancel");
    await m.click(m.byText(".dialog-btn", /^Cancel$/));
    assert.equal(m.container.find(".confirm"), null);
    assert.equal(m.sent("quit").length, 0);
    await m.key("q", { ctrlKey: true, altKey: true });
    await m.click(m.byText(".dialog-btn", /Quit and stop everything/));
    assert.equal(m.sent("quit").length, 1);
  }));

test("a window reached through the relay has no update entry and sends none of these", () =>
  withPage({ remote: true, state: { update: UPDATE } }, async (m) => {
    assert.equal(m.container.find(".rail-updates"), null);
    await m.key("q", { ctrlKey: true, shiftKey: true });
    assert.match(m.container.find(".notice").textContent, new RegExp(RELAY_WORDS.slice(0, 30)));
    await m.key("q", { ctrlKey: true, altKey: true });
    assert.equal(m.container.find(".confirm"), null);
    for (const c of ["detach", "quit", "restart", "updates", "checkForUpdate", "listVersions", "installVersion"]) {
      assert.equal(m.store.send({ cmd: c }), false, c);
      assert.equal(m.sent(c).length, 0, c);
    }
  }));

test("the page asks the desktop to save as it is hidden", () => {
  const handlers = {};
  const win = { addEventListener: (t, f) => { handlers[t] = f; }, removeEventListener() {} };
  const calls = [];
  watchPageLifecycle(win, { start: () => calls.push("start"), stop: () => calls.push("stop"), send: (c) => calls.push(c.cmd) });
  handlers.pagehide({});
  assert.deepEqual(calls, ["save", "stop"]);
});

test("while the quit question is open no key reaches an action", () =>
  withPage({}, async (m) => {
    await m.key("q", { ctrlKey: true, altKey: true });
    assert.ok(m.container.find(".confirm"));
    await m.key("q", { ctrlKey: true, shiftKey: true });
    assert.equal(m.sent("detach").length, 0);
    assert.ok(m.container.find(".confirm"));
  }));

test("while the Updates dialog is open no key reaches an action", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-updates", /Updates/));
    await m.key("q", { ctrlKey: true, shiftKey: true });
    await m.key("q", { ctrlKey: true, altKey: true });
    assert.equal(m.sent("detach").length, 0);
    assert.equal(m.container.find(".confirm"), null);
    assert.ok(m.container.find(".dialog"));
  }));

test("a hello that says remote after the first render still makes quit and detach say the relay words", () =>
  withPage({ remote: true, stateFirst: true }, async (m) => {
    await m.key("q", { ctrlKey: true, altKey: true });
    assert.equal(m.container.find(".confirm"), null);
    assert.match(m.container.find(".notice").textContent, new RegExp(RELAY_WORDS.slice(0, 30)));
    await m.key("q", { ctrlKey: true, shiftKey: true });
    assert.equal(m.sent("detach").length, 0);
  }));

test("a second look at the versions does not show the last answer while the new one loads", () =>
  withPage({}, async (m) => {
    await m.click(m.byText(".rail-updates", /Updates/));
    await m.click(m.byText(".dialog-btn", /Install a specific version/));
    await m.push({ type: "versions", items: [{ version: "v-old-answer", relation: "older" }] });
    assert.match(m.container.find(".dialog").textContent, /v-old-answer/);
    await m.click(m.byText(".dialog-btn", /^Close$/));
    await m.click(m.byText(".rail-updates", /Updates/));
    await m.click(m.byText(".dialog-btn", /Install a specific version/));
    assert.equal(m.sent("listVersions").length, 2);
    assert.doesNotMatch(m.container.find(".dialog").textContent, /v-old-answer/);
    assert.match(m.container.find(".dialog").textContent, /Loading/);
    await m.push({ type: "versions", items: [{ version: "v-new-answer", relation: "newer" }] });
    assert.match(m.container.find(".dialog").textContent, /v-new-answer/);
  }));

// The fake DOM keeps a checkbox's state as the property or as the attribute.
const on = (box) => !!(box.checked ?? box.attributes.checked);

test("the switch starts on when updates are on and off when the preference says off, and follows a prefs push", () => {
  const run = (prefs, fn) => withPage({ prefs }, async (m) => {
    await m.click(m.byText(".rail-updates", /Updates/));
    await fn(m, m.container.find("input"));
  });
  return run({}, async (m, box) => {
    assert.equal(on(box), true);
  }).then(() => run({ updatesOff: true }, async (m, box) => {
    assert.equal(on(box), false);
    await m.push({ type: "prefs", prefs: {} });
    assert.equal(on(m.container.find("input")), true);
  }));
});

test("a notice from before the dialog opened is not shown in it, a newer one is", () =>
  withPage({}, async (m) => {
    await m.push({ type: "notice", text: "STALE-NOTICE" });
    await m.click(m.byText(".rail-updates", /Updates/));
    assert.doesNotMatch(m.container.find(".dialog").textContent, /STALE-NOTICE/);
    await m.push({ type: "notice", text: "FRESH-NOTICE" });
    assert.match(m.container.find(".dialog").textContent, /FRESH-NOTICE/);
  }));
