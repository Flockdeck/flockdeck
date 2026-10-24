// Find in terminal: a bar over the terminal of the pane in the drawer that
// searches what the terminal holds, scrollback included. Enter (and F3) goes to
// the next match, Shift+Enter to the one before, Escape closes the bar, clears
// the marks and gives the keyboard back to the terminal.
//
// The search is xterm's own (its search addon, loaded the first time the bar
// opens); the controller does the finding and reports "3 of 12" back through
// matches. The bar says so when nothing matched, since a failed search changes
// nothing else on the screen.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "../shell/keys.js";

/** countText says which match is showing and how many there are, as the search
 *  reports them. The addon stops counting past its highlight limit (a negative
 *  count). */
export function countText(query, matches) {
  if (!query || !matches) return "";
  if (matches.count > 0) return (matches.index + 1) + " of " + matches.count;
  if (matches.count === 0) return "No matches";
  return "Many matches";
}

/** FindBar is the bar. ctl is the terminal controller of the pane (createTerminal),
 *  name the pane's name for the label, matches the latest { index, count } or null,
 *  initial the words last searched for, askSeq a number that grows each time the
 *  person asks for the bar again while it is open (it takes the keyboard back), and
 *  onClose(words) closes it. */
export function FindBar({ ctl, name, matches, initial = "", askSeq = 0, onClose }) {
  const [query, setQuery] = useState(initial);
  const [ready, setReady] = useState(null);
  const [miss, setMiss] = useState(false);
  const input = useRef(null);
  const take = () => {
    const el = input.current;
    if (!el) return;
    if (el.focus) el.focus();
    if (el.select) el.select();
  };
  useEffect(() => {
    take();
    let live = true;
    ctl.searchReady().then((ok) => {
      if (!live) return;
      setReady(ok);
      if (ok && query) setMiss(ctl.find(query, { typing: true }) === false);
    });
    return () => {
      live = false;
      ctl.clearFind();
    };
  }, [ctl]);
  useEffect(() => {
    if (askSeq > 0) take();
  }, [askSeq]);

  const step = (back) => {
    const found = ctl.find(query, { back });
    setMiss(found === false);
  };
  const close = () => {
    onClose(query);
    ctl.focus();
  };
  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      close();
    } else if ((e.key === LIST.accept && e.target === input.current) || e.key === LIST.step) {
      e.preventDefault();
      e.stopPropagation();
      step(!!e.shiftKey);
    }
  };
  const count = countText(query, matches);
  return html`
    <div class="find-bar" role="search" aria-label=${name ? "Find in " + name : "Find in the terminal"} onKeyDown=${onKeyDown}>
      <label class="find-label" for="find-input">${name ? "Find in " + name : "Find"}</label>
      <input id="find-input" class=${"find-field" + (miss ? " is-miss" : "")} type="text" autocomplete="off" spellcheck="false" ref=${input}
        value=${query} aria-invalid=${miss ? "true" : "false"} aria-describedby="find-count"
        onInput=${(e) => {
          const v = e.target.value;
          setQuery(v);
          setMiss(ctl.find(v, { typing: true }) === false);
        }} />
      <span class="find-count" id="find-count" role="status">${ready === false ? "Search could not be loaded" : count}</span>
      <button type="button" class="drawer-btn find-prev" aria-label="Previous match" onClick=${() => step(true)}>Previous</button>
      <button type="button" class="drawer-btn find-next" aria-label="Next match" onClick=${() => step(false)}>Next</button>
      <button type="button" class="drawer-btn find-close" aria-label="Close find" onClick=${close}>Close</button>
    </div>
  `;
}
