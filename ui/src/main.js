// Entry point of the new shell, loaded by index.html as a module. All imports
// are relative, and nothing here is evaluated from text.
//
// It makes the store of the control socket, starts it, and renders the shell
// over it. A window reached through the relay registers its end-to-end key when
// its hello arrives (watchRemoteHello loads e2e.js for that, which the page does
// not carry), and sends no graph command: the store refuses them there.
import { render } from "./vendor/preact.mjs";
import { html } from "./html.js";
import { createStore } from "./lib/store.js";
import { controlUrl } from "./lib/socket.js";
import { loadKeyDefaults } from "./lib/keytable.js";
import { watchRemoteHello } from "./lib/e2e-boot.js";
import { watchPageLifecycle } from "./lib/page-lifecycle.js";
import { Live } from "./shell/Live.js";

const store = createStore({ url: controlUrl(window.location), keyDefaults: () => loadKeyDefaults(window.fetch.bind(window), window.location) });
watchRemoteHello(store);
store.start();
watchPageLifecycle(window, store);

render(
  html`<${Live} store=${store} matchMedia=${window.matchMedia.bind(window)} />`,
  document.getElementById("app"),
);
