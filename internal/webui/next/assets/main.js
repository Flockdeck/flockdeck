// Entry point of the new shell, loaded by index.html as a module. All imports
// are relative, and nothing here is evaluated from text.
//
// There is no connection to the server yet, so the shell is drawn from an empty
// hello and state, with the hello component in its main area. The socket and
// store replace these two objects.
import { render } from "./vendor/preact.mjs";
import { html } from "./html.js";
import { Hello } from "./hello.js";
import { Shell } from "./shell/app.js";

const hello = { keys: [] };
const state = { root: "", projects: [] };

render(
  html`<${Shell} hello=${hello} state=${state} matchMedia=${window.matchMedia.bind(window)}><${Hello} /><//>`,
  document.getElementById("app"),
);
