// Draws the new page over the frames graphfixture sent (the file named on the
// command line: hello, state and graph, in the order the fake socket sent them)
// and prints what is on the screen. Run by the Go test, from the node harness of
// ui. Nothing here opens a network connection.
import { readFileSync } from "node:fs";
import { render } from "../../../ui/src/vendor/preact.mjs";
import { html } from "../../../ui/src/html.js";
import { Live } from "../../../ui/src/shell/Live.js";
import { createDom, install, settle } from "../../../ui/test/dom.mjs";
import { FakeWebSocket, harness } from "../../../ui/test/fakes.mjs";

const frames = JSON.parse(readFileSync(process.argv[2], "utf8"));
const dom = createDom();
const restore = install(dom);
const { store } = await harness();
const container = dom.document.createElement("div");
dom.body.appendChild(container);
store.start();
render(html`<${Live} store=${store} window=${dom.window} />`, container);
await settle();
const ws = FakeWebSocket.last;
ws.open();
for (const f of frames) {
  ws.frame(f);
  await settle();
}
const out = {
  watches: ws.commands("graphWatch").length,
  boxes: container.findAll("g").filter((e) => e.getAttribute("role") === "button").map((e) => e.getAttribute("data-pane")),
  drawers: container.findAll("[data-drawer-pane]").length,
  drawerName: container.find(".drawer-name") ? container.find(".drawer-name").textContent : null,
  bar: !!container.find(".mbar"),
  homeText: container.find(".home") ? container.find(".home").textContent.slice(0, 200) : null,
};
console.log(JSON.stringify(out));
render(null, container);
store.stop();
restore();
