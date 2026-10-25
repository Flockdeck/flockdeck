// The files and diffs of one branch in the team-wide Changes view: the file list, the
// picked file's diff with its review comments, and the list of all the comments.
// It reads the diffs the store has kept (lib/changesdiffs.js), asks for a file's diff
// when one is picked, and sends comments with the store's send.
//
// - win: { viewer, relay, caps } says what this window may do (actsFor in
//   drawer/diff/gateModel.js). Without the changesDiff cap only the file list is
//   drawn. Without the changesComments cap, or in a viewer or relay window, the
//   comments are drawn and nothing can be written or closed (readOnly).
// - diffs, results, session, connected: the store's changesDiffs, changesTeamResults,
//   session (it changes with each hello) and whether the connection is open. A comment is sent with the
//   head the diff was read at (the entry's drawnSha), not the head the listing has now.
import { useEffect, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { FileList } from "../drawer/diff/FileList.js";
import { DiffViewer } from "../drawer/diff/DiffViewer.js";
import { CommentDrawer } from "../drawer/diff/Comments.js";
import { useCommentFlow } from "../drawer/diff/commentFlow.js";
import { actsFor } from "../drawer/diff/gateModel.js";
import { diffKey } from "../lib/changesdiffs.js";

const NO_DIFFS = {};
const NO_RESULTS = [];

export function BranchDiff({ branch: b, names = {}, win = {}, diffs = NO_DIFFS, results = NO_RESULTS, session = null, connected = true, store = null }) {
  const [picked, setPicked] = useState("");
  const [layout, setLayout] = useState("unified");
  const files = b.files;
  const path = files.some((f) => f.path === picked) ? picked : files.length ? files[0].path : "";
  const file = files.find((f) => f.path === path) || null;
  const hasDiff = !win.relay && Array.isArray(win.caps) && win.caps.includes("changesDiff");
  const canWrite = actsFor("changesComments", win);
  const entry = diffs[diffKey(b.pane, path)] || null;
  useEffect(() => {
    if (!hasDiff || !store || !path || entry) return;
    store.readChangesDiff({ cmd: "diffHunks", id: b.pane, file: path });
  }, [b.pane, path, hasDiff, !!entry]);
  const flow = useCommentFlow({
    pane: b.pane, drawnSha: entry ? entry.drawnSha : "", comments: b.comments, results, connected, session,
    send: (cmd) => !!store && store.send(cmd),
  });
  const nameOf = (p) => names[p] || p;
  const askContext = (gap, ask) => store && store.readChangesDiff({ cmd: "diffContext", id: b.pane, file: path, side: ask.side, from: ask.from, to: ask.to }, gap);

  if (!hasDiff) {
    return html`<${FileList} files=${files} selected=${path} onPick=${setPicked} comments=${b.comments} />`;
  }
  return html`<div class="ct-diffs">
    <${FileList} files=${files} selected=${path} onPick=${setPicked} comments=${flow.comments} />
    <p class="ct-layout"><button type="button" class="drawer-btn" aria-pressed=${layout === "split" ? "true" : "false"} onClick=${() => setLayout(layout === "split" ? "unified" : "split")}>Side by side</button></p>
    <${DiffViewer} key=${b.pane + "\n" + path} branch=${b.branch} file=${file} hunks=${entry ? entry.hunks : null} layout=${layout}
      flow=${flow} readOnly=${!canWrite} headSha=${b.headSha} nameOf=${nameOf}
      context=${entry ? entry.context : undefined} onContext=${askContext} />
    <${CommentDrawer} comments=${flow.comments} headSha=${b.headSha} errors=${flow.errors} closing=${flow.closing} nameOf=${nameOf}
      onJump=${(c) => setPicked(c.path)} onResolve=${canWrite ? flow.close : null} onReopen=${canWrite ? flow.reopen : null} />
  </div>`;
}
