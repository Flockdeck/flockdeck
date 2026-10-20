import { html } from "./html.js";

// Hello is the first component of the new shell. It stands until the real ones
// replace it, and it is what the page-load test and the harness's component
// test draw.
export function Hello({ name = "Flockdeck" }) {
  return html`<h1 class="hello">Hello, ${name}</h1>`;
}
