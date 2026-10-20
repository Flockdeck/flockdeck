// Entry point of the new shell, loaded by index.html as a module. All imports
// are relative, and nothing here is evaluated from text.
import { render } from "./vendor/preact.mjs";
import { html } from "./html.js";
import { Hello } from "./hello.js";

render(html`<${Hello} />`, document.getElementById("app"));
