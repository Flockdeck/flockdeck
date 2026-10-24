// The Changes tab (C6): the list, a diff per file, commit, push and the question
// about a repository's programs, drawn from the store's state.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Changes, LIST_FIRST } from "../assets/drawer/Changes.js";
import { createDom, install, settle } from "./dom.mjs";

const file = (path, over = {}) => ({ path, status: "M", label: "modified", added: 1, removed: 1, stamp: "s", ...over });
const ch = (over = {}) => ({ cwd: "/r", branch: "main", upstream: "origin/main", ahead: 1, behind: 0, hasRemote: true, omitted: 0, files: [file("a.go")], ...over });
const fakeStore = () => {
  const calls = [];
  return {
    calls,
    requestChanges(...a) { calls.push(["changes", ...a]); return true; },
    requestDiff(f) { calls.push(["diff", f]); return true; },
    commitChanges(m, p) { calls.push(["commit", m, p]); return m.trim() ? "" : "A commit message is required"; },
    remoteChanges(a) { calls.push(["remote", a]); return true; },
    answerGitWarn(a) { calls.push(["warn", a]); return true; },
    clearChanges() { calls.push(["clear"]); },
  };
};
const draw = (snap) => renderToString(html`<${Changes} cwd="/r" snap=${snap} store=${fakeStore()} />`);

async function mount(snap, fn, st = fakeStore()) {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${Changes} cwd="/r" snap=${snap} store=${st} />`, box);
    await settle();
    await fn(box, st, (s) => render(html`<${Changes} cwd="/r" snap=${s} store=${st} />`, box));
  } finally {
    restore();
  }
}

test("the list shows each kind of change by the label the desktop gave it", () => {
  const out = draw({ changes: ch({ files: [file("m.go"), file("new.txt", { status: "?", label: "new", untracked: true }), file("gone.go", { status: "D", label: "deleted" }), file("to.go", { status: "R", label: "renamed" }), file("img.png", { status: "M", label: "modified", added: 0, removed: 0 })] }) });
  for (const w of ["m.go", "new.txt", "gone.go", "to.go", "img.png", "deleted", "renamed", "new"]) assert.ok(out.includes(w), w);
  assert.ok(out.includes("Commit 5 files"));
});

test("a tree with thousands of changed files draws a bounded list and says how many are not listed", () => {
  const files = Array.from({ length: 2000 }, (_, i) => file("f" + i + ".go"));
  const out = draw({ changes: ch({ files, omitted: 3000 }) });
  assert.equal(out.split('class="chg-file"').length - 1, LIST_FIRST);
  assert.ok(out.includes("3,000 more files are not listed. The commit includes them."));
  assert.ok(out.includes("Commit 5,000 files"));
  assert.ok(out.includes("1,800 not drawn yet"));
});

test("a clean tree says so and offers no commit", () => {
  const out = draw({ changes: ch({ files: [] }) });
  assert.ok(out.includes("Nothing has changed."));
  assert.ok(!out.includes("Commit message"));
});

test("a path with markup, and git's own words, are drawn as text", () => {
  const evil = "<img src=x onerror=alert(1)>.go";
  const out = draw({ changes: ch({ files: [file(evil)] }), changesNote: { text: "fatal: <script>x</script>", error: true } });
  assert.ok(!out.includes("<img") && !out.includes("<script"));
  assert.ok(out.includes("fatal: &lt;script"));
  assert.ok(!/href=/.test(out), "a path was put in a link");
});

test("not a working tree shows the reason, and the repositories just inside it", async () => {
  const out = draw({ changes: { cwd: "/r", error: "not a git repository", repos: [{ name: "inner", path: "/r/inner" }] } });
  assert.ok(out.includes("not a git repository") && out.includes("inner"));
  await mount({ changes: { cwd: "/r", error: "x", repos: [{ name: "inner", path: "/r/inner" }] } }, (box, st) => {
    box.querySelector(".chg-repo").click();
    assert.deepEqual(st.calls.at(-1), ["changes", "/r/inner", false]);
  });
});

test("opening asks for the tree and closing lets go", async () => {
  const st = fakeStore();
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${Changes} cwd="/r" snap=${{}} store=${st} />`, box);
    await settle();
    assert.deepEqual(st.calls, [["changes", "/r", false]]);
    assert.ok(box.textContent.includes("Reading what changed"));
    render(null, box);
    await settle();
    assert.deepEqual(st.calls.at(-1), ["clear"]);
  } finally {
    restore();
  }
});

test("picking a file asks for its diff and draws it, and picking it again hides it", async () => {
  const snap = { changes: ch() };
  await mount(snap, async (box, st, redraw) => {
    box.querySelector(".chg-file-btn").click();
    await settle();
    assert.deepEqual(st.calls.at(-1), ["diff", "a.go"]);
    assert.ok(box.textContent.includes("Reading the diff"));
    redraw({ ...snap, diff: { file: "a.go", text: "@@ -1 +1 @@\n-a\n+b\n", error: "" } });
    await settle();
    assert.ok(box.querySelector("[data-diff]"));
    redraw({ ...snap, diff: { file: "a.go", text: "", error: "git failed" } });
    await settle();
    assert.ok(box.textContent.includes("git failed"));
    box.querySelector(".chg-file-btn").click();
    await settle();
    assert.equal(box.querySelector(".chg-diff"), null);
  });
});

test("a commit with no message shows the reason and the message is sent when there is one", async () => {
  await mount({ changes: ch() }, async (box, st) => {
    box.querySelector(".chg-commit-btn").click();
    await settle();
    assert.deepEqual(st.calls.at(-1), ["commit", "", false]);
    assert.ok(box.textContent.includes("A commit message is required"));
    const area = box.querySelector("#chg-message");
    area.value = "Fix it";
    area.dispatch("input");
    await settle();
    box.querySelector(".chg-commit-push").click();
    await settle();
    assert.deepEqual(st.calls.at(-1), ["commit", "Fix it", true]);
  });
});

test("push, pull and fetch go to the store, and are disabled while an action is out", async () => {
  await mount({ changes: ch() }, (box, st) => {
    box.querySelector(".chg-push").click();
    box.querySelector(".chg-pull").click();
    box.querySelector(".chg-fetch").click();
    assert.deepEqual(st.calls.slice(-3), [["remote", "push"], ["remote", "pull"], ["remote", "fetch"]]);
  });
  const out = draw({ changes: ch(), changesBusy: "push" });
  assert.ok(out.includes("Working: push"));
  assert.equal((out.match(/disabled/g) || []).length >= 5, true, "buttons were live while a push ran");
});

test("a tree with no remote offers no push, pull or commit and push", () => {
  const out = draw({ changes: ch({ hasRemote: false }) });
  assert.ok(!out.includes("chg-push") && !out.includes("chg-commit-push"));
});

test("a question about the repository's programs lists them and asks twice before going on", async () => {
  const warn = { seq: 1, intro: "This repository runs programs.", items: ["hook: <b>pre-commit</b>"], seen: "z" };
  const out = draw({ changes: ch(), gitWarn: warn });
  assert.ok(out.includes("This repository runs programs.") && out.includes("hook: &lt;b") && !out.includes("<b>pre"));
  await mount({ changes: ch(), gitWarn: warn }, async (box, st) => {
    assert.equal(box.querySelector(".chg-warn-remember"), null, "remember was offered before continuing");
    box.querySelector(".chg-warn-continue").click();
    await settle();
    box.querySelector(".chg-warn-remember").click();
    assert.deepEqual(st.calls.at(-1), ["warn", "remember"]);
  });
  await mount({ changes: ch(), gitWarn: warn }, (box, st) => {
    box.querySelector(".chg-warn-stop").click();
    assert.deepEqual(st.calls.at(-1), ["warn", ""]);
  });
});
