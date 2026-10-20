// html is htm bound to Preact's h, so a component is written as a template
// literal: html`<p>${text}</p>`. Text put in with ${} is a child and so is
// drawn as text, never parsed as markup.
import { h } from "./vendor/preact.mjs";
import htm from "./vendor/htm.mjs";

export const html = htm.bind(h);
