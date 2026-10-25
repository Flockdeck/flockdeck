// The help: a dialog with a search box, the contents and the page beside it. F1
// opens it (the "help" action of the key table). The pages are fetched once, as
// nodes, and drawn as elements: no markup is ever parsed here (lib/help.js
// checks their shape first). The first entry is a page the window makes itself,
// the key table as this desktop has it, with what this window runs marked.
import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { useInert } from "./inert.js";
import { h } from "../vendor/preact.mjs";
import { html } from "../html.js";
import { composing, listMove, LIST } from "./keys.js";
import { trapTab } from "./focus.js";
import { keySections, keysEntry, searchPages } from "../lib/help.js";

/** NOT_YET is what the keys page says of an action this window does not run. */
export const NOT_YET = "not in this window yet";

/** PALETTE_ONLY stands where an action has no key of its own. */
export const PALETTE_ONLY = "Command palette";

/** Tree draws the nodes of a page. onPage is called with the slug of a link to
 *  another page. */
export function Tree({ nodes, onPage }) {
  return nodes.map((n, i) => {
    if (!n.t) return n.x;
    const kids = n.c && n.c.length ? html`<${Tree} nodes=${n.c} onPage=${onPage} />` : null;
    if (n.t === "a") {
      if (n.h.startsWith("#")) {
        const slug = n.h.slice(1);
        return h("a", {
          key: i,
          href: n.h,
          onClick: (e) => {
            e.preventDefault();
            if (onPage) onPage(slug);
          },
        }, kids);
      }
      return h("a", { key: i, href: n.h, target: "_blank", rel: "noopener noreferrer" }, kids);
    }
    return h(n.t, { key: i }, kids);
  });
}

/** KeysPage draws the key table: one table to a section, each action with its
 *  key (or that it is in the palette), whether this window runs it, and a way to
 *  the page that explains it. */
export function KeysPage({ keys, implemented, known, onPage }) {
  const have = new Set(implemented || []);
  const sections = keySections(keys);
  if (sections.length === 0) {
    return html`<p>This window has not been sent the key table yet.</p>`;
  }
  return html`
    <div class="help-keys">
      <h1>Keys in this window</h1>
      <p>These are the keys set on this desktop. An action marked as not in this window yet is listed in the command palette and does nothing here until it is built.</p>
      ${sections.map((s) => html`
        <section key=${s.name}>
          <h2>${s.name}</h2>
          <table>
            <thead><tr><th>Keys</th><th>Action</th><th>Here</th></tr></thead>
            <tbody>
              ${s.rows.map((k) => html`
                <tr key=${k.id} data-action=${k.id}>
                  <td>${k.keys ? html`<kbd>${k.keys}</kbd>` : PALETTE_ONLY}</td>
                  <td>${k.label || k.id}${k.page && known.has(k.page)
                    ? html` <a href=${"#" + k.page} onClick=${(e) => { e.preventDefault(); onPage(k.page); }}>More</a>`
                    : null}</td>
                  <td>${have.has(k.id) ? "yes" : NOT_YET}</td>
                </tr>`)}
            </tbody>
          </table>
        </section>`)}
    </div>`;
}

/** Help is the dialog. keys and implemented are the window's (the hello's key
 *  table, and the actions it runs). load resolves to { pages } or { error } (see
 *  lib/help.js). slug is the page to open on. onClose is called when the person
 *  leaves it by Escape or by a press outside. */
export function Help({ keys, implemented, load, slug = "", onClose }) {
  const [loaded, setLoaded] = useState(null);
  const [query, setQuery] = useState("");
  const [shown, setShown] = useState(slug);
  const [attempt, setAttempt] = useState(0);
  // The name of a link that is no page, said instead of doing nothing.
  const [missing, setMissing] = useState("");
  const search = useRef(null);
  const content = useRef(null);
  // First, so on close the page is live again before the focus effect below puts the focus back.
  const root = useRef(null);
  useInert(root);

  useEffect(() => {
    let live = true;
    setLoaded(null);
    load().then((r) => {
      if (live) setLoaded(r);
    });
    return () => {
      live = false;
    };
  }, [attempt]);

  // The keyboard goes to the search box when the dialog opens and back to where
  // it was when it closes.
  useEffect(() => {
    const el = search.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    if (el && el.focus) el.focus();
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);

  const entries = useMemo(() => (loaded && loaded.pages ? [keysEntry(keys)].concat(loaded.pages) : []), [loaded, keys]);
  const hits = useMemo(() => searchPages(entries, query), [entries, query]);
  const known = useMemo(() => new Set(entries.map((e) => e.slug)), [entries]);

  // Searching narrows the contents. A page the search has hidden gives way to
  // the first thing that matched, and a name that is no page gives way to the
  // first page.
  const listed = hits.some((x) => x.page.slug === shown);
  const page = (listed && entries.find((e) => e.slug === shown)) || (hits[0] && hits[0].page) || entries[0] || null;
  const at = Math.max(0, hits.findIndex((x) => x.page === page));

  const pick = (s) => {
    if (!known.has(s)) {
      setMissing(s);
      return;
    }
    setMissing("");
    setShown(s);
    setQuery("");
    if (content.current && content.current.scrollTo) content.current.scrollTo(0, 0);
  };

  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.tab && root.current) {
      // Tab stays inside the dialog, as in Dialog.js.
      trapTab(e, root.current);
      return;
    }
    const move = listMove(e, at, hits.length);
    if (move === "cancel") {
      e.preventDefault();
      // Escape first empties a search that has text, then closes.
      if (query) setQuery("");
      else if (onClose) onClose();
    } else if (typeof move === "number" && e.target === search.current && hits.length > 0) {
      // The arrows walk the contents from the search box, as in a list.
      e.preventDefault();
      setMissing("");
      setShown(hits[move].page.slug);
    }
  };

  let body = null;
  if (!loaded) {
    body = html`<p class="help-note">Loading…</p>`;
  } else if (loaded.error) {
    body = html`<div class="help-note">
      <p role="alert">${loaded.error}</p>
      <button type="button" class="help-retry" onClick=${() => setAttempt(attempt + 1)}>Try again</button>
    </div>`;
  } else if (page && page.keys) {
    body = html`<${KeysPage} keys=${keys} implemented=${implemented} known=${known} onPage=${pick} />`;
  } else if (page) {
    body = html`<${Tree} nodes=${page.tree} onPage=${pick} />`;
  }

  return html`
    <div class="help-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose && onClose()}>
      <div class="help" role="dialog" aria-modal="true" aria-label="Help" ref=${root} onKeyDown=${onKeyDown}>
        <nav class="help-nav" aria-label="Help contents">
          <input
            ref=${search}
            class="help-search"
            type="search"
            aria-label="Search the help"
            placeholder="Search the help"
            autocomplete="off"
            spellcheck="false"
            value=${query}
            onInput=${(e) => { setMissing(""); setQuery(e.target.value); }}
          />
          ${loaded && loaded.pages
            ? hits.length === 0
              ? html`<p class="help-none">Nothing here matches.</p>`
              : html`<ul class="help-list">
                  ${hits.map((x) => html`
                    <li key=${x.page.slug}>
                      <button type="button" class=${"help-item" + (x.page === page ? " is-current" : "")}
                        aria-current=${x.page === page ? "page" : undefined} data-page=${x.page.slug}
                        onClick=${() => pick(x.page.slug)}>
                        <span class="help-item-title">${x.page.title}</span>
                        <span class="help-item-sub">${x.snippet || x.page.summary}</span>
                      </button>
                    </li>`)}
                </ul>`
            : null}
        </nav>
        <div class="help-content" ref=${content} tabindex="0" role="region" aria-label=${page ? page.title : "Help"} data-page=${page ? page.slug : ""}>${missing ? html`<p class="help-note" role="status">There is no help page called "${missing}".</p>` : null}${body}</div>
      </div>
    </div>`;
}
