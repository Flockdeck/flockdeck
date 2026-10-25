// The files and diffs of one branch in the team-wide Changes view: the file list, the
// picked file's diff with its review comments, and the list of all the comments.
// It reads the diffs the store has kept (lib/changesdiffs.js), asks for a file's diff
// when one is picked, and sends comments with the store's send.
//
// - win: { viewer, relay, caps } says what this window may do (actsFor in
//   drawer/diff/gateModel.js). Without the changesDiff cap only the file list is
//   drawn. Without the changesComments cap, or in a viewer or relay window, the
//   comments are drawn and nothing can be written or closed (readOnly).
// - A held diff is current only for the head and the file size it was read at. When the
//   branch moves on (or the file's counts change) it is not drawn: the note says the
//   branch changed, the diff is read again, and a comment box open on the old hunks is
//   closed with its text kept. A read that could not be asked for, or came back as an
//   error, says so with a Retry button; an error is tried again after a new hello.
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
import { diffKey, statOf } from "../lib/changesdiffs.js";

const NO_DIFFS = {};
const NO_RESULTS = [];
const NO_CONTEXT = {};

export function BranchDiff({ branch: b, names = {}, win = {}, diffs = NO_DIFFS, results = NO_RESULTS, session = null, connected = true, store = null }) {
  const [picked, setPicked] = useState("");
  const [layout, setLayout] = useState("unified");
  const [askFailed, setAskFailed] = useState(false);
  const [tries, setTries] = useState(0);
  const files = b.files;
  const path = files.some((f) => f.path === picked) ? picked : files.length ? files[0].path : "";
  const file = files.find((f) => f.path === path) || null;
  const hasDiff = !win.relay && !win.viewer && Array.isArray(win.caps) && win.caps.includes("changesDiff");
  const canWrite = actsFor("changesComments", win);
  const entry = diffs[diffKey(b.pane, path)] || null;
  const stale = !!entry && (entry.drawnSha !== b.headSha || entry.stat !== statOf(file));
  const state = !entry ? "none" : stale ? "stale" : "ok";
  const current = state === "ok" ? entry : null;

  useEffect(() => {
    if (!hasDiff || !store || !path || state === "ok") {
      setAskFailed(false);
      return;
    }
    setAskFailed(!store.readChangesDiff({ cmd: "diffHunks", id: b.pane, file: path }));
  }, [b.pane, path, hasDiff, connected, session, b.headSha, state, tries]);
  useEffect(() => {
    if (store && entry) store.touchChangesDiff(b.pane, path);
  }, [b.pane, path, entry ? entry.seq : 0]);
  // Leaving the branch lets go of what was read for it.
  useEffect(() => () => { if (store) store.dropChangesDiffs(b.pane); }, [b.pane]);

  const flow = useCommentFlow({
    pane: b.pane, drawnSha: current ? current.drawnSha : "", comments: b.comments, results, connected, session,
    diffId: current ? current.seq : -1,
    send: (cmd) => !!store && store.send(cmd),
  });
  const nameOf = (p) => names[p] || p;
  const askContext = (gap, ask) => store && store.readChangesDiff({ cmd: "diffContext", id: b.pane, file: path, side: ask.side, from: ask.from, to: ask.to }, gap);
  const retry = () => {
    if (store) store.dropChangesDiffs(b.pane, path);
    setTries(tries + 1);
  };
  const jump = (c) => {
    const f = files.find((x) => x.path === c.path || (x.oldPath && x.oldPath === c.path));
    if (f) setPicked(f.path);
  };

  if (!hasDiff) {
    return html`<${FileList} files=${files} selected=${path} onPick=${setPicked} comments=${b.comments} />`;
  }
  const failed = current && current.hunks && current.hunks.error;
  const note = state === "stale" ? "The branch changed; reading the diff again." : askFailed ? "The diff could not be asked for; try again." : "";
  return html`<div class="ct-diffs">
    <${FileList} files=${files} selected=${path} onPick=${setPicked} comments=${flow.comments} />
    <p class="ct-layout"><button type="button" class="drawer-btn" aria-pressed=${layout === "split" ? "true" : "false"} onClick=${() => setLayout(layout === "split" ? "unified" : "split")}>Side by side</button></p>
    <p class="ct-diffnote view-note" role="status">${note}</p>
    ${askFailed || failed ? html`<p><button type="button" class="drawer-btn ct-retry" onClick=${retry}>Retry</button></p>` : null}
    <${DiffViewer} key=${b.pane + ":" + path} branch=${b.branch} file=${file} hunks=${current ? current.hunks : null} layout=${layout}
      flow=${flow} readOnly=${!canWrite} headSha=${b.headSha} nameOf=${nameOf}
      context=${current ? current.context : NO_CONTEXT} contextErrors=${current ? current.contextErrors : NO_CONTEXT} onContext=${askContext} />
    <${CommentDrawer} comments=${flow.comments} headSha=${b.headSha} errors=${flow.errors} closing=${flow.closing} nameOf=${nameOf}
      onJump=${jump} onResolve=${canWrite ? flow.close : null} onReopen=${canWrite ? flow.reopen : null} />
  </div>`;
}
