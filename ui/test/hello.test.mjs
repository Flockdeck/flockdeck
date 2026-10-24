// A component test: the component is drawn with Preact to a string, which needs
// no browser. render-to-string lives in test/vendor and is never shipped.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Hello } from "../src/hello.js";

test("Hello draws a greeting", () => {
  const out = renderToString(html`<${Hello} />`);
  assert.equal(out, '<h1 class="hello">Hello, Flockdeck</h1>');
});

test("Hello takes a name", () => {
  const out = renderToString(html`<${Hello} name="Ada" />`);
  assert.match(out, />Hello, Ada</);
});

// Text from an agent, a task or a message is untrusted. A child put in with
// ${} is text, so markup in it comes out escaped.
test("Hello draws a name that holds markup as text", () => {
  const name = '<img src=x onerror="alert(1)">';
  const out = renderToString(html`<${Hello} name=${name} />`);
  assert.ok(!out.includes("<img"), out);
  assert.match(out, /&lt;img src=x onerror=/);
});
