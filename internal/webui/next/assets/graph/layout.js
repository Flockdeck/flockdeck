// The tidy-tree layout of the team graph: a pure function from the view model
// (graph/model.js) to positions. There is no DOM, no clock and no randomness
// here, so the same model always gives the same boxes.
//
// What it draws:
//
// - Each team is a frame. Its manager is the topmost box in it and its helpers
//   hang below, nested helpers deeper. Teams sit side by side, in the graph's
//   order.
// - A subtree is a block. A node is centred over its children, and the children
//   blocks are packed left to right into rows no wider than rowWidth, then the
//   next row below. So eleven helpers are a block of three rows, not one row of
//   2,400 pixels, and no box can lie over another: every block holds only its
//   own boxes and blocks in one row are apart by gapX.
// - Members a team's tree does not reach (model teams[].unlinked) are placed in
//   rows below the tree, so none vanishes.
// - The panes in no team (model.solo, and any node that no team or solo list
//   holds) are the strip along the bottom.
//
// Nothing is recursive, so a chain of any length is laid out.

/** The sizes the views use, in SVG units. A caller may override any of them. */
export const DEFAULTS = {
  nodeW: 216,
  nodeH: 128,
  gapX: 24,
  gapY: 48,
  pad: 16,
  teamHead: 52,
  teamGap: 40,
  stripGap: 48,
  stripHead: 52,
  // The widest a row of blocks gets before the next block starts a new row.
  rowWidth: 4 * 216 + 3 * 24,
};

/** An empty layout, for a model that has nothing to draw. */
export function emptyLayout(opts = DEFAULTS) {
  return { ok: false, opts, width: 0, height: 0, teams: [], nodes: [], byPane: new Map(), solo: null };
}

// The blocks are packed into rows no wider than limit. A block wider than the
// limit has a row of its own. Returns the rows (lists of block indexes, as
// {at, x} entries with x counted from the row's left), each row's width and
// height, and the whole area's width and height.
function packRows(sizes, limit, gapX, gapY) {
  const rows = [];
  let cur = null;
  for (let i = 0; i < sizes.length; i++) {
    const s = sizes[i];
    if (cur === null || cur.w + gapX + s.w > limit) {
      cur = { items: [], w: 0, h: 0 };
      rows.push(cur);
    }
    cur.items.push({ at: i, x: cur.items.length === 0 ? 0 : cur.w + gapX });
    cur.w = cur.items.length === 1 ? s.w : cur.w + gapX + s.w;
    if (s.h > cur.h) cur.h = s.h;
  }
  let w = 0;
  let h = 0;
  for (const r of rows) {
    if (r.w > w) w = r.w;
    h += r.h;
  }
  if (rows.length > 1) h += gapY * (rows.length - 1);
  return { rows, w, h };
}

// Lays out the tree of one team. Returns its boxes as {pane, x, y, depth}
// relative to the block's top left, and the block's size.
function layoutTree(tree, o) {
  // Flatten in pre-order, so a parent comes before its descendants and the
  // reverse order has every child before its parent.
  const flat = [];
  const stack = [{ t: tree, parent: -1 }];
  while (stack.length > 0) {
    const { t, parent } = stack.pop();
    const idx = flat.length;
    flat.push({ t, parent, kids: [], size: null, pack: null, x: 0, y: 0 });
    if (parent >= 0) flat[parent].kids.push(idx);
    // Pushed backwards so that the first child is taken first.
    for (let i = t.children.length - 1; i >= 0; i--) stack.push({ t: t.children[i], parent: idx });
  }
  // kids were pushed in the order the stack popped them, which is the order of
  // the children, because each is pushed in reverse.
  for (let i = flat.length - 1; i >= 0; i--) {
    const f = flat[i];
    if (f.kids.length === 0) {
      f.size = { w: o.nodeW, h: o.nodeH };
      continue;
    }
    f.pack = packRows(
      f.kids.map((k) => flat[k].size),
      o.rowWidth,
      o.gapX,
      o.gapY,
    );
    f.size = { w: Math.max(o.nodeW, f.pack.w), h: o.nodeH + o.gapY + f.pack.h };
  }
  // Top down: a block's origin is set by its parent's rows.
  flat[0].x = 0;
  flat[0].y = 0;
  const boxes = [];
  for (let i = 0; i < flat.length; i++) {
    const f = flat[i];
    boxes.push({ pane: f.t.pane, x: f.x + (f.size.w - o.nodeW) / 2, y: f.y, depth: f.t.depth });
    if (f.pack === null) continue;
    let rowY = f.y + o.nodeH + o.gapY;
    for (const row of f.pack.rows) {
      const left = f.x + (f.size.w - row.w) / 2;
      for (const it of row.items) {
        const k = flat[f.kids[it.at]];
        k.x = left + it.x;
        k.y = rowY;
      }
      rowY += row.h + o.gapY;
    }
  }
  return { boxes, w: flat[0].size.w, h: flat[0].size.h };
}

// Leaves in rows under a title, as the unlinked members and the solo strip are.
function layoutRows(panes, limit, o) {
  const pack = packRows(
    panes.map(() => ({ w: o.nodeW, h: o.nodeH })),
    limit,
    o.gapX,
    o.gapY / 2,
  );
  const boxes = [];
  let y = 0;
  for (const row of pack.rows) {
    for (const it of row.items) boxes.push({ pane: panes[it.at], x: it.x, y, depth: 0 });
    y += row.h + o.gapY / 2;
  }
  return { boxes, w: pack.w, h: pack.h };
}

/**
 * layoutGraph places the boxes of a graph model.
 *
 * Returns { ok, opts, width, height, teams, nodes, byPane, solo }. teams has
 * one frame per team, { id, x, y, w, h, manager, bodyY }, and nodes one box per
 * pane, { pane, x, y, w, h, team, depth }, teams first in the model's order and
 * then the solo strip. byPane is the boxes by pane id. solo is the strip's
 * frame, or null when there is none. Every pane of the model has exactly one
 * box.
 */
export function layoutGraph(model, options = {}) {
  const o = { ...DEFAULTS, ...options };
  if (!model || !model.ok) return emptyLayout(o);

  const nodes = [];
  const byPane = new Map();
  const place = (box, team) => {
    const b = { pane: box.pane, x: box.x, y: box.y, w: o.nodeW, h: o.nodeH, team, depth: box.depth };
    nodes.push(b);
    byPane.set(b.pane, b);
  };
  const placed = new Set();

  const teams = [];
  let cursor = 0;
  let teamsH = 0;
  for (const team of model.teams) {
    const parts = [];
    let w = 0;
    let h = 0;
    if (team.tree !== null) {
      const t = layoutTree(team.tree, o);
      parts.push({ boxes: t.boxes, y: 0, centre: true, w: t.w });
      w = t.w;
      h = t.h;
    }
    const loose = team.unlinked.map((n) => n.pane);
    if (loose.length > 0) {
      const r = layoutRows(loose, Math.max(o.rowWidth, w), o);
      const y = h === 0 ? 0 : h + o.gapY;
      parts.push({ boxes: r.boxes, y, centre: false, w: r.w });
      w = Math.max(w, r.w);
      h = y + r.h;
    }
    const frameW = Math.max(w, o.nodeW) + 2 * o.pad;
    const frameH = o.teamHead + h + 2 * o.pad;
    const frame = { id: team.id, x: cursor, y: 0, w: frameW, h: frameH, manager: team.manager, bodyY: o.teamHead + o.pad };
    for (const part of parts) {
      // The tree is centred in the frame; the loose rows start at the left.
      const dx = part.centre ? o.pad + (frameW - 2 * o.pad - part.w) / 2 : o.pad;
      for (const b of part.boxes) {
        if (placed.has(b.pane)) continue;
        placed.add(b.pane);
        place({ pane: b.pane, x: cursor + dx + b.x, y: frame.bodyY + part.y + b.y, depth: b.depth }, team.id);
      }
    }
    teams.push(frame);
    cursor += frameW + o.teamGap;
    if (frameH > teamsH) teamsH = frameH;
  }
  const teamsW = teams.length === 0 ? 0 : cursor - o.teamGap;

  // The strip: the solo panes, then any node that no team holds, so that none
  // is left off the page.
  const strip = [];
  const inStrip = new Set();
  const addToStrip = (n) => {
    if (placed.has(n.pane) || inStrip.has(n.pane)) return;
    inStrip.add(n.pane);
    strip.push(n.pane);
  };
  for (const n of model.solo) addToStrip(n);
  for (const n of model.nodes) addToStrip(n);
  let solo = null;
  let height = teamsH;
  let width = teamsW;
  if (strip.length > 0) {
    const limit = Math.max(teamsW - 2 * o.pad, o.rowWidth);
    const r = layoutRows(strip, limit, o);
    const frameW = Math.max(r.w, o.nodeW) + 2 * o.pad;
    const y = teamsH === 0 ? 0 : teamsH + o.stripGap;
    solo = { x: 0, y, w: frameW, h: o.stripHead + r.h + 2 * o.pad, bodyY: o.stripHead + o.pad };
    for (const b of r.boxes) place({ pane: b.pane, x: o.pad + b.x, y: y + solo.bodyY + b.y, depth: 0 }, "");
    height = y + solo.h;
    width = Math.max(width, frameW);
  }
  return { ok: true, opts: o, width, height, teams, nodes, byPane, solo };
}
