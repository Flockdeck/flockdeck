// The joined script the server sends (ui/build) does what the
// modules do. TestNodeTests joins the modules and names the file in
// NEXT_BUNDLE_JS; run alone, with no such file, this skips.
import { test } from "node:test";
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, hello } from "./fakes.mjs";

test("the joined script starts the shell, draws it and talks to the socket as main.js does", { skip: !process.env.NEXT_BUNDLE_JS && "NEXT_BUNDLE_JS is not set" }, async () => {
  const dom = createDom();
  const restore = install(dom);
  const had = globalThis.WebSocket;
  try {
    const app = dom.document.createElement("div");
    app.id = "app";
    dom.body.appendChild(app);
    dom.document.getElementById = (id) => (id === "app" ? app : null);
    dom.window.location = { href: "http://127.0.0.1:1/", pathname: "/", protocol: "http:", search: "", hash: "" };
    globalThis.WebSocket = FakeWebSocket;
    FakeWebSocket.reset();
    await import(pathToFileURL(process.env.NEXT_BUNDLE_JS).href);
    await settle();
    const ws = FakeWebSocket.last;
    assert.ok(ws, "the joined script opened no socket");
    assert.match(ws.url, /\/ws\/control$/);
    ws.open();
    ws.frame(hello());
    await settle();
    assert.ok(app.childNodes.length > 0, "the joined script drew nothing");
  } finally {
    if (had === undefined) delete globalThis.WebSocket;
    else globalThis.WebSocket = had;
    restore();
  }
});
