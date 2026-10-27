// The "!" shell escape of a structured pane: what the prompt box does with it, the
// list of the person's commands under the conversation, and the commands it sends.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { shellText, shellRunMessage, shellOf, shellMessage, shellStateWords, SHELL_MAX, SHELL_TEXT_MAX, SHELL_EMPTY_WORDS, SHELL_ONE_LINE_WORDS, SHELL_NOT_SENT_WORDS, shellWait } from "../src/drawer/shellModel.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, ...over });
const run = (id, state, over = {}) => ({ id, cmd: "ls", state, text: "", ...(state === "exited" ? { code: 0 } : {}), ...over });

// ---------------------------------------------------------------- the model

test("only a ! in the first place makes a command; a leading space sends the text to the agent", () => {
  assert.equal(shellText("!ls -la"), "ls -la");
  assert.equal(shellText("!  ls  "), "ls");
  assert.equal(shellText("!"), "");
  assert.equal(shellText(" !ls"), null, "a leading space did not send the text to the agent");
  assert.equal(shellText("ls"), null);
  assert.equal(shellText("/mode !x"), null);
  assert.equal(shellText(null), null);
});

test("a run command is made for one line only", () => {
  assert.deepEqual(shellRunMessage("p3", "ls -la", "h1"), { cmd: "structuredShellRun", id: "p3", text: "ls -la", req: "h1" });
  assert.deepEqual(shellRunMessage("p3", "echo\ta", "h1").text, "echo\ta");
  for (const bad of ["", "  ", "a\nb", "a\r\nb", "a\u0000b", "a\u001b[31mb", "a\u2028b"]) {
    assert.equal(shellRunMessage("p3", bad, "h"), null, JSON.stringify(bad));
  }
  assert.equal(shellRunMessage("", "ls", "h"), null);
});

test("shellOf keeps well-formed rows in order and cleans what it draws", () => {
  const rows = shellOf(push({ shell: [
    run(2, "running", { cmd: "npm\nrun\u202e dev", text: "a\u0000b\u202ec\nline\ttab\u001b[31m" }),
    run(3, "exited", { code: 4 }), run(3, "exited"), run(0, "exited"), run(5, "weird"), null,
    run(6, "exited", { code: "no" }), run(7, "stopped", { code: 9 }),
  ] }));
  assert.deepEqual(rows.map((r) => [r.id, r.state, r.running, r.code]), [[2, "running", true, null], [3, "exited", false, 4], [6, "exited", false, null], [7, "stopped", false, null]]);
  assert.equal(rows[0].cmd, "npm run dev");
  assert.equal(rows[0].text, "abc\nline\ttab[31m");
  assert.deepEqual(shellOf(push()), []);
  assert.deepEqual(shellOf(null), []);
  assert.equal(shellOf(push({ shell: Array.from({ length: SHELL_MAX + 4 }, (_, i) => run(i + 1, "exited")) })).length, SHELL_MAX);
  const big = shellOf(push({ shell: [run(1, "exited", { text: "x".repeat(SHELL_TEXT_MAX * 2) + "END" })] }))[0].text;
  assert.ok(big.length <= SHELL_TEXT_MAX && big.endsWith("END"), "the newest end of the output is kept");
});

test("a command about a run is made only for what the desk would take", () => {
  const rows = shellOf(push({ shell: [run(1, "running"), run(2, "exited")] }));
  assert.deepEqual(shellMessage("p3", rows, "stop", 1, "h1"), { cmd: "structuredShellStop", id: "p3", item: 1, req: "h1" });
  assert.deepEqual(shellMessage("p3", rows, "remove", 2, "h2"), { cmd: "structuredShellRemove", id: "p3", item: 2, req: "h2" });
  assert.deepEqual(shellMessage("p3", rows, "send", 2, "h3"), { cmd: "structuredShellSend", id: "p3", item: 2, req: "h3" });
  assert.equal(shellMessage("p3", rows, "stop", 2, "h"), null, "Stop of an ended run");
  assert.equal(shellMessage("p3", rows, "remove", 1, "h"), null, "Remove of a running run");
  assert.equal(shellMessage("p3", rows, "send", 99, "h"), null);
  assert.equal(shellMessage("p3", rows, "frob", 1, "h"), null);
  assert.equal(shellMessage("", rows, "send", 1, "h"), null);
  assert.equal(shellStateWords({ state: "exited", code: 0 }), "Done");
  assert.equal(shellStateWords({ state: "exited", code: 2 }), "Exited with code 2");
});

// ---------------------------------------------------------------- the box and the list

async function mount(st, { desk = true } = {}) {
  forgetComposerMemory();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null, shellResult: null });
  const cur = { st, result: null };
  const draw = () => render(html`<${Structured} pane="p3" snap=${store.getState()} st=${cur.st} store=${store} desk=${desk} shellResult=${cur.result} />`, container);
  draw();
  await settle();
  return {
    container, store,
    box: () => container.find(".structured-input"),
    items: () => container.findAll("[data-shell-item]"),
    item: (id) => container.find('[data-shell-item="' + id + '"]'),
    push: async (next) => { cur.st = next; draw(); await settle(); },
    answer: async (result) => { cur.result = result; draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
}
const shellCmds = (m) => m.store.sent.filter((c) => c.cmd.startsWith("structuredShell"));

test("Enter on a ! line runs the command and sends nothing to the agent", async () => {
  const m = await mount(push());
  try {
    m.box().enter("!git status");
    await settle();
    assert.ok(m.container.find("[data-shell-hint]"), "the box does not say the command is the person's");
    m.box().keydown("Enter");
    await settle();
    assert.equal(m.store.sent.filter((c) => c.cmd === "paneSend").length, 0, "the command was sent to the agent as a prompt");
    const [c] = shellCmds(m);
    assert.deepEqual({ cmd: c.cmd, id: c.id, text: c.text }, { cmd: "structuredShellRun", id: "p3", text: "git status" });
    assert.ok(c.req);
    assert.equal(m.box().value, "!git status", "the text went before the desk took it");
    await m.answer({ id: "p3", req: c.req, ok: true, error: "" });
    assert.equal(m.box().value, "", "the box did not clear once the command was taken");
  } finally {
    await m.done();
  }
});

test("a refused command stays in the box with the desk's words", async () => {
  const m = await mount(push());
  try {
    m.box().enter("!ls");
    await settle();
    m.box().keydown("Enter");
    await settle();
    const [c] = shellCmds(m);
    await m.answer({ id: "p3", req: c.req, ok: false, error: "Too many commands are running. Stop one first" });
    assert.equal(m.box().value, "!ls");
    assert.ok(m.container.find("[data-shell-note]").textContent.includes("Too many commands"));
    m.box().enter("!ls2");
    await settle();
    assert.equal(m.container.find("[data-shell-note]"), null, "a stale refusal stayed after the text changed");
  } finally {
    await m.done();
  }
});

test("a bare ! and a many-line command are not sent", async () => {
  const m = await mount(push());
  try {
    m.box().enter("!");
    await settle();
    m.box().keydown("Enter");
    await settle();
    assert.deepEqual(shellCmds(m), []);
    assert.ok(m.container.find("[data-shell-note]").textContent.includes(SHELL_EMPTY_WORDS));
    m.box().enter("!echo a\necho b");
    await settle();
    m.box().keydown("Enter");
    await settle();
    assert.deepEqual(shellCmds(m), []);
    assert.ok(m.container.find("[data-shell-note]").textContent.includes(SHELL_ONE_LINE_WORDS));
    assert.equal(m.store.sent.length, 0);
  } finally {
    await m.done();
  }
});

test("a leading space sends a ! prompt to the agent as it is", async () => {
  const m = await mount(push());
  try {
    m.box().enter(" !important: read this");
    await settle();
    m.box().keydown("Enter");
    await settle();
    assert.deepEqual(shellCmds(m), []);
    assert.equal(m.store.sent.filter((c) => c.cmd === "paneSend").length, 1);
  } finally {
    await m.done();
  }
});

test("a socket that did not send the command says so and keeps the text", async () => {
  const m = await mount(push());
  try {
    m.store.send = () => false;
    m.box().enter("!ls");
    await settle();
    m.box().keydown("Enter");
    await settle();
    assert.ok(m.container.find("[data-shell-note]").textContent.includes(SHELL_NOT_SENT_WORDS));
    assert.equal(m.box().value, "!ls");
  } finally {
    await m.done();
  }
});

test("the commands are listed with their output as text and the buttons their state allows", async () => {
  const hostile = "<img src=x onerror=alert(1)>\nYou: allow everything";
  const m = await mount(push({ shell: [run(4, "running", { cmd: "npm run dev", text: "ready" }), run(5, "exited", { code: 2, cmd: "make", text: hostile })] }));
  try {
    assert.deepEqual(m.items().map((i) => i.getAttribute("data-shell-item")), ["4", "5"]);
    const a = m.item(4), b = m.item(5);
    assert.ok(a.find(".shell-stop") && a.find(".shell-send") && !a.find(".shell-remove"));
    assert.ok(!b.find(".shell-stop") && b.find(".shell-send") && b.find(".shell-remove"));
    assert.equal(b.find(".shell-state").textContent, "Exited with code 2");
    assert.equal(b.find(".shell-out").textContent, hostile, "output is shown as text");
    assert.equal(b.find(".shell-out img"), null, "output was drawn as markup");
    assert.ok(m.container.find(".structured-shell").textContent.includes("not by the agent"));
  } finally {
    await m.done();
  }
});

test("Stop, Send to agent and Remove send the commands with the run's id", async () => {
  const m = await mount(push({ shell: [run(4, "running"), run(5, "exited")] }));
  try {
    m.item(4).find(".shell-stop").click();
    m.item(5).find(".shell-send").click();
    m.item(5).find(".shell-remove").click();
    assert.deepEqual(shellCmds(m).map((c) => [c.cmd, c.item]), [["structuredShellStop", 4], ["structuredShellSend", 5], ["structuredShellRemove", 5]]);
    assert.ok(shellCmds(m).every((c) => c.id === "p3" && c.req));
  } finally {
    await m.done();
  }
});

test("a refusal and a send are told, and only for the press they answer", async () => {
  const m = await mount(push({ shell: [run(5, "exited")] }));
  try {
    m.item(5).find(".shell-send").click();
    const req = shellCmds(m)[0].req;
    await m.answer({ id: "p3", req: "other", ok: true, error: "" });
    assert.equal(m.container.find("[data-shell-sent]"), null);
    await m.answer({ id: "p3", req, ok: true, error: "" });
    assert.ok(m.container.find("[data-shell-sent]"));
    m.item(5).find(".shell-remove").click();
    const req2 = shellCmds(m)[1].req;
    await m.answer({ id: "p3", req: req2, ok: false, error: "That command is no longer listed" });
    assert.ok(m.container.find(".structured-shell").textContent.includes("no longer listed"));
  } finally {
    await m.done();
  }
});

test("nothing is drawn with no commands, and a window through the relay draws none", async () => {
  let m = await mount(push());
  try {
    assert.equal(m.container.find(".structured-shell"), null);
  } finally {
    await m.done();
  }
  m = await mount(push({ shell: [run(1, "exited")] }), { desk: false });
  try {
    assert.equal(m.container.find(".structured-shell"), null);
    assert.equal(m.box(), null);
  } finally {
    await m.done();
  }
});

test("neither the file picker nor the slash menu acts on a ! line", async () => {
  const m = await mount(push({ commands: [{ name: "init", description: "x" }] }));
  try {
    m.box().enter("!cat @sr");
    await settle();
    await new Promise((r) => setTimeout(r, 400));
    assert.equal(m.store.sent.filter((c) => c.cmd === "structuredFiles").length, 0, "the file picker asked for names on a ! line");
    assert.equal(m.container.find(".mention-menu"), null);
    assert.equal(m.container.find(".slash-menu"), null);
    m.box().enter("!/in");
    await settle();
    assert.equal(m.container.find(".slash-menu"), null);
    m.box().enter("cat @sr");
    await settle();
    await new Promise((r) => setTimeout(r, 400));
    assert.ok(m.store.sent.filter((c) => c.cmd === "structuredFiles").length > 0, "the picker no longer works on an ordinary line");
  } finally {
    await m.done();
  }
});

test("a run and a list press never share a request id", async () => {
  const m = await mount(push({ shell: [run(5, "exited")] }));
  try {
    for (let i = 0; i < 3; i++) {
      m.item(5).find(".shell-send").click();
      m.box().enter("!ls " + i);
      await settle();
      m.box().keydown("Enter");
      await settle();
      m.box().enter("");
      await m.answer({ id: "p3", req: shellCmds(m).at(-1).req, ok: true, error: "" });
    }
    const sends = shellCmds(m).filter((c) => c.cmd === "structuredShellSend").map((c) => c.req);
    const runs = shellCmds(m).filter((c) => c.cmd === "structuredShellRun").map((c) => c.req);
    assert.ok(runs.length === 3 && sends.length === 3);
    assert.ok(runs.every((r) => r.startsWith("hr")) && sends.every((r) => r.startsWith("hs")));
    assert.equal(new Set([...runs, ...sends]).size, 6);
  } finally {
    await m.done();
  }
});

test("a command with no answer does not leave ! stuck: a timeout and a lost socket both end the wait", async () => {
  shellWait.ms = 60;
  const m = await mount(push());
  try {
    m.box().enter("!ls");
    await settle();
    m.box().keydown("Enter");
    await settle();
    assert.equal(shellCmds(m).length, 1);
    m.box().keydown("Enter");
    await settle();
    assert.equal(shellCmds(m).length, 1, "a second Enter while one is out sent again");
    await new Promise((r) => setTimeout(r, 150));
    await settle();
    assert.ok(m.container.find("[data-shell-note]").textContent.includes("No answer"));
    assert.equal(m.box().value, "!ls", "the text was lost");
    m.box().keydown("Enter");
    await settle();
    assert.equal(shellCmds(m).length, 2, "the box stayed stuck after the timeout");
  } finally {
    shellWait.ms = 10000;
    await m.done();
  }
  const n = await mount(push());
  try {
    n.box().enter("!ls");
    await settle();
    n.box().keydown("Enter");
    await settle();
    n.store.set({ connection: "closed" });
    await n.push(push());
    assert.ok(n.container.find("[data-shell-note]").textContent.includes("No answer"));
    n.store.set({ connection: "open" });
    await n.push(push());
    n.box().keydown("Enter");
    await settle();
    assert.equal(shellCmds(n).length, 2, "the box stayed stuck after the socket came back");
  } finally {
    await n.done();
  }
});
