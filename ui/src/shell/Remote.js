// The Remote section of the settings: whether this machine is reachable through
// the relay, how to pair a phone or browser, and the devices already paired.
//
// What is asked of the person, and what the dialog never does:
//   - A pairing link works once and expires. The dialog says until when and counts
//     down, and when the time has passed the link and its picture are taken away
//     and it says so, rather than leaving a dead link to be copied. While a link
//     is on screen the roster is asked for again, so a device paired meanwhile
//     shows up.
//   - Unpairing, turning remote access off, removing this machine, moving it to
//     another relay and marking a device verified are each put as a question first,
//     with what stays and what goes.
//   - A window reached through the relay may list the devices, pair one, rename and
//     unpair. It is told that turning remote access on or off, moving it, a join
//     code, verifying a device and what a device may do are the desk's, and none
//     of them is offered there. The store does not send them and the desktop
//     refuses them.
//
// Names (of devices, machines and this one) and the relay's own sentences came from
// people and from the relay: they are drawn as text. The pairing picture is an
// image whose source is the SVG the desktop drew, so nothing in it runs.
import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Confirm } from "./Confirm.js";
import {
  ACCESS, DESK_ONLY_ACCESS, DESK_ONLY_JOIN, DESK_ONLY_REMOTE, ENTERPRISE_WORDS, INTRO_WORDS, KEPT_WORDS, LINK_WARNING, PAIR_WORDS, PLAN_WORDS, POLL_MS,
  VERIFY_HELP, VERIFY_HELP_RELAY, accessOf, addsAccess, accessIndex, ago, bare, deviceCodes, devicesCommand, enableCommand, leftText, moveCommand, nextAccess, outcomeOf, pairCommand, joinLine, pairLive, pairOf,
  deviceName, planText, qrSource, renameCommand, resumeCommand, revokeCommand, roleCommand, rosterOf, summaryOf, unverifyCommand, untilText, verifyCommand, verifyText, viewOf,
} from "../lib/remote.js";

const NEW_DRAFT = { relay: "", name: "", join: "", invite: "", more: false };
const NEW_MOVE = { open: false, relay: "", invite: "", join: "" };

/** Remote is the section. status is the tunnel as the state message gives it (or
 *  null while remote access is off), roster, pair and outcome are the store's
 *  latest answers, remote says this window was reached through the relay, send is
 *  the store's send and copy(text, what) puts text on the clipboard. news is the
 *  latest notice since the dialog opened. tickMs, pollMs and clock are for tests. */
export function Remote({ status, roster, pair, outcome, remote, send, copy, forget = () => {}, news = "", tickMs = 1000, pollMs = POLL_MS, clock = () => Date.now() }) {
  const [now, setNow] = useState(clock());
  const [busy, setBusy] = useState(null);
  const [asking, setAsking] = useState(null);
  const [renaming, setRenaming] = useState(null);
  const [open, setOpen] = useState(() => new Set());
  const [draft, setDraft] = useState(NEW_DRAFT);
  const [moving, setMoving] = useState(NEW_MOVE);
  const [problem, setProblem] = useState("");
  const [said, setSaid] = useState("");
  const [link, setLink] = useState(null);
  const [join, setJoin] = useState(null);
  const [wanting, setWanting] = useState("");
  const renameField = useRef(null);
  const seenPair = useRef(pair ? pair.seq : 0);
  // The codes last sent, kept for a minute and only here, to put back in the form if the relay answers with an error,
  // so a typo is not a retype of the rest. A success, the minute, or closing the dialog drops them.
  const sentCodes = useRef({});
  useEffect(() => () => { sentCodes.current = {}; }, []);
  const outcomeFloor = useRef(outcome ? outcome.seq : 0);
  const outcomeSeq = outcome ? outcome.seq : 0;

  const r = viewOf(status);
  const ro = useMemo(() => (roster ? rosterOf(roster) : null), [roster]);
  const o = outcome && outcome.seq > outcomeFloor.current ? outcomeOf(outcome) : null;
  useEffect(() => {
    if (!o) return;
    const sent = sentCodes.current[o.action];
    delete sentCodes.current[o.action];
    if (!sent || !o.error || Date.now() - sent.at > 60000) return;
    if (o.action === "enable") setDraft((d) => ({ ...d, join: d.join || sent.join, invite: d.invite || sent.invite }));
    if (o.action === "move") setMoving((m) => ({ ...m, join: m.join || sent.join, invite: m.invite || sent.invite }));
  }, [outcome && outcome.seq]);
  const working = busy && !(outcome && outcome.seq > busy.floor) ? busy.action : "";
  const off = !!r && r.state === "disabled";
  const live = pairLive(link, now);
  const joinOk = pairLive(join, now);

  useEffect(() => {
    if (send(devicesCommand()) === false) setProblem("The request could not be sent. Check the connection.");
  }, []);
  // The name field takes the keyboard when it opens.
  const renamingKey = renaming ? renaming.kind + ":" + renaming.id : "";
  useEffect(() => {
    if (renamingKey && renameField.current && renameField.current.focus) renameField.current.focus();
  }, [renamingKey]);
  // A pairing answer is for a device or for another desktop; each is kept in its
  // own place so that asking for one does not take the other away.
  useEffect(() => {
    if (!pair || pair.seq <= seenPair.current) return;
    seenPair.current = pair.seq;
    const p = pairOf(pair);
    p.arrived = clock();
    if (p.kind === "host") {
      setJoin(p);
      setWanting((w) => (w === "host" ? "" : w));
    } else {
      setLink(p);
      setWanting((w) => (w === "device" ? "" : w));
      setNow(clock());
    }
  }, [pair]);
  // The store keeps the last answer for the life of the page; it is let go when the
  // link has run out and when the dialog closes, so a stale one is never offered again.
  const spent = (!!link && !live) || (!!join && !joinOk);
  useEffect(() => {
    if (spent) forget();
  }, [spent]);
  useEffect(() => forget, []);
  // While a link is good to scan the clock ticks, to count down to its end and to
  // ask for the roster again every few seconds; nothing else asks again on its own.
  useEffect(() => {
    if (!live && !joinOk) return undefined;
    let n = 0;
    const every = Math.max(1, Math.round(pollMs / tickMs));
    const t = setInterval(() => {
      setNow(clock());
      n += 1;
      if (n % every === 0) send(devicesCommand());
    }, tickMs);
    return () => clearInterval(t);
  }, [live, joinOk]);

  const go = (cmd, action = "") => {
    if (!cmd) return false;
    const ok = send(cmd) !== false;
    setProblem(ok ? "" : "The request could not be sent. Check the connection.");
    if (ok && action) setBusy({ action, floor: outcomeSeq });
    return ok;
  };
  const ask = (at, title, words, label, run, device = null) => setAsking({ at, title, words, label, run, device });
  // A question about a device's access names the level the device had when it was asked. If the roster then shows another
  // level, or no device, the question is no longer true: it is closed, and a late click on it sends nothing.
  const accessNow = (id) => {
    const cur = ro && ro.devices.find((x) => x.id === id);
    return cur ? accessIndex(cur) : null;
  };
  useEffect(() => {
    if (!asking || !asking.device || accessNow(asking.device.id) === asking.device.level) return;
    setAsking(null);
    setSaid("");
    setProblem("Access for " + asking.device.name + " changed while you were deciding. Nothing was sent. Look at it again.");
  }, [ro]);
  const confirmFor = (at) =>
    asking && asking.at === at
      ? html`<${Confirm} title=${asking.title} words=${asking.words} label=${asking.label}
          onConfirm=${() => {
            const q = asking;
            setAsking(null);
            if (q.device && accessNow(q.device.id) !== q.device.level) {
              setProblem("Access for " + q.device.name + " changed while you were deciding. Nothing was sent. Look at it again.");
              return;
            }
            q.run();
          }} onCancel=${() => setAsking(null)} />`
      : null;
  const pairNow = (kind) => {
    if (go(pairCommand(kind))) setWanting(kind);
  };
  const sayEnter = (fn) => (e) => {
    if (composing(e)) return;
    if (e.key === LIST.accept) {
      e.preventDefault();
      fn();
    }
  };

  // --- not enrolled -------------------------------------------------------
  const notOn = !r && ro && !ro.enabled;
  // The codes are secrets: once sent they are not kept in the form.
  const enable = () => {
    if (go(enableCommand(draft), "enable")) {
      sentCodes.current.enable = { join: draft.join, invite: draft.invite, at: Date.now() };
      setDraft({ ...draft, join: "", invite: "" });
    }
  };
  const enableForm = () => {
    const field = (key, id, placeholder, label, secret = false) => html`<input type=${secret ? "password" : "text"} class="dialog-field" id=${id} placeholder=${placeholder} aria-label=${label}
      autocomplete="off" spellcheck="false" value=${draft[key]} onInput=${(e) => setDraft({ ...draft, [key]: e.target.value })}
      onKeyDown=${sayEnter(() => enable())} />`;
    return html`
      <div class="remote-box" id="remote-enable-box">
        <p class="set-intro">This machine is not enrolled with a relay. Leave the relay empty for remote.flockdeck.ai, or name another.</p>
        <div class="remote-form">
          ${field("relay", "remote-relay", "Relay — empty for remote.flockdeck.ai", "Relay address")}
          ${field("name", "remote-name", "Name for this machine — empty for its host name", "Machine name")}
          ${draft.more ? field("join", "remote-join", "Join code, from a machine already on the account", "Join code", true) : null}
          ${draft.more ? field("invite", "remote-invite", "Invitation code, for a relay that asks for one", "Invitation code", true) : null}
        </div>
        <div class="remote-actions">
          <button type="button" class="dialog-btn dialog-btn--primary" id="remote-enable" disabled=${!!working} onClick=${() => enable()}>
            ${working === "enable" ? "Turning it on…" : "Turn on Flockdeck Remote"}
          </button>
          <button type="button" class="dialog-btn" id="remote-more" aria-expanded=${draft.more ? "true" : "false"}
            onClick=${() => setDraft({ ...draft, more: !draft.more })}>${draft.more ? "No codes" : "Joining an account, or invited?"}</button>
        </div>
        ${o && o.action === "enable" && o.error ? html`<p class="set-error" role="alert">${o.error}</p>` : null}
        ${o && o.warning ? html`<p class="set-error" role="alert">${o.warning}</p>` : null}
        <p class="set-intro">The same from a terminal: flockdeck remote enable.</p>
        <p class="set-intro">${ENTERPRISE_WORDS}</p>
      </div>`;
  };

  // --- pairing ------------------------------------------------------------
  const pairBox = () => {
    const waiting = wanting === "device";
    const expired = !!link && !!link.url && !live;
    return html`
      <section class="remote-sec" aria-labelledby="remote-pair-head">
        <h4 class="set-sub" id="remote-pair-head">Pair a device</h4>
        ${link && link.error ? html`<p class="set-error" role="alert">${link.error}</p>` : null}
        ${live
          ? html`<div class="remote-pair" id="remote-link-box">
              ${link.qr ? html`<img class="remote-qr" alt="QR code for the pairing link" src=${qrSource(link.qr)} />` : null}
              <div class="remote-pair-text">
                <p>Scan this with the device you want to pair, or open the link on it. It works once${untilText(link.expiresAt)}.</p>
                ${link.expiresAt ? html`<p class="remote-left" id="remote-left" role="timer">${leftText(link.expiresAt, now)}</p>` : null}
                <input type="text" class="dialog-field remote-link" id="remote-link" readOnly aria-label="Pairing link" value=${link.url}
                  onFocus=${(e) => e.target.select && e.target.select()} />
                <button type="button" class="dialog-btn" id="remote-copy" onClick=${() => copy(link.url, "the pairing link")}>Copy link</button>
                <p class="set-about">${LINK_WARNING}</p>
              </div>
            </div>`
          : expired
            ? html`<p class="set-error" id="remote-expired" role="status">That pairing link has expired. Ask for a new one.</p>`
            : waiting ? null : html`<p class="set-intro">${PAIR_WORDS}</p>`}
        <div class="remote-actions">
          <button type="button" class="dialog-btn dialog-btn--primary" id="remote-pair" disabled=${waiting} onClick=${() => pairNow("device")}>
            ${waiting ? "Asking the relay…" : live || expired ? "New link" : "Pair a device"}
          </button>
        </div>
      </section>`;
  };

  // --- devices ------------------------------------------------------------
  const codeBlock = (d, k) => {
    const name = deviceName(d.name) || "this device";
    const sure = () => ask("device:" + d.id, "Mark " + name + " verified?", ["Only do this if the code above is exactly what " + name + " shows."], "Mark verified", () => go(verifyCommand(d.id, k.origin, k.code)));
    return html`<div class="remote-code" data-origin=${k.origin}>
      <div class="key-note">${k.label} · ${verifyText(k.state)}</div>
      <p class="remote-fingerprint">${k.code}</p>
      ${remote
        ? null
        : k.state === "verified"
          ? html`<button type="button" class="dialog-btn" onClick=${() => go(unverifyCommand(d.id, k.origin))}>Remove verification</button>`
          : html`<button type="button" class="dialog-btn dialog-btn--primary" aria-label=${"Mark the " + k.label + " code verified for " + name} onClick=${sure}>Mark verified</button>`}
    </div>`;
  };
  const renameBox = (kind, id, current) => {
    const save = () => {
      const next = renaming.value.trim();
      setRenaming(null);
      if (next !== current) go(renameCommand(kind, id, next));
    };
    return html`<div class="remote-rename">
      <input type="text" class="dialog-field" id="remote-rename-field" aria-label=${kind === "host" ? "New name for this machine" : "New name for " + (deviceName(current) || "this device")}
        autocomplete="off" spellcheck="false" value=${renaming.value} ref=${renameField}
        onInput=${(e) => setRenaming({ ...renaming, value: e.target.value })}
        onKeyDown=${(e) => {
          if (composing(e)) return;
          if (e.key === LIST.accept) { e.preventDefault(); save(); }
          else if (e.key === LIST.cancel) { e.preventDefault(); e.stopPropagation(); setRenaming(null); }
        }} />
      <button type="button" class="dialog-btn" id="remote-rename-save" onClick=${save}>Save</button>
      <button type="button" class="dialog-btn" onClick=${() => setRenaming(null)}>Cancel</button>
    </div>`;
  };
  const deviceRow = (d) => {
    const mine = !!ro.current && d.id === ro.current;
    const codes = deviceCodes(d);
    const shown = open.has(d.id);
    const name = deviceName(d.name) || "Unnamed device";
    const level = accessOf(d);
    const next = nextAccess(d);
    const setLevel = (to) => {
      if (go(roleCommand(d.id, to))) setSaid("Asked the desktop to set " + name + " to “" + to.label + "”.");
    };
    const about = { id: d.id, level: accessIndex(d), name };
    const here = renaming && renaming.kind === "device" && renaming.id === d.id;
    return html`<li class="remote-row" key=${d.id} data-device=${d.id}>
      <div class="key-main">
        <div class="key-title"><span class="key-name">${name}</span>${mine ? html`<span class="key-id">this device</span>` : null}</div>
        <div class="key-status">paired ${ago(d.created, now)} · last seen ${ago(d.lastSeen, now)}</div>
        ${codes.length && shown ? codes.map((k) => codeBlock(d, k)) : null}
      </div>
      <div class="key-actions">
        ${codes.length
          ? html`<button type="button" class="dialog-btn" aria-expanded=${shown ? "true" : "false"} aria-label=${(shown ? "Hide the code for " : "Verify the code for ") + name}
              onClick=${() => setOpen((s) => { const n = new Set(s); if (n.has(d.id)) n.delete(d.id); else n.add(d.id); return n; })}>${shown ? "Hide code" : "Verify"}</button>`
          : null}
        ${remote
          ? null
          : next
            ? html`<button type="button" class="dialog-btn remote-access" aria-label=${"Access for " + name + ": " + level.label + ". Press to change it to " + next.label + "."}
                onClick=${() => {
                  if (!addsAccess(level, next)) setLevel(next);
                  else ask("access:" + d.id, "Give " + name + " more access?", ["It goes from “" + level.label + "” to “" + next.label + "”."], "Give access", () => setLevel(next), about);
                }}>${level.label}</button>`
            : html`<button type="button" class="dialog-btn remote-access" aria-label=${"Access for " + name + ": unknown, which the desktop treats as view-only. Press to set it to " + ACCESS[1].label + "."}
                onClick=${() => ask("access:" + d.id, "Reset access for " + name + "?", ["Its access is not one this page knows, and the desktop treats it as view-only. It will be set to “" + ACCESS[1].label + "”."], "Reset access", () => setLevel(ACCESS[1]), about)}>${level.label}</button>`}
        <button type="button" class="dialog-btn" onClick=${() => setRenaming({ kind: "device", id: d.id, value: d.name })}>Rename</button>
        <button type="button" class="dialog-btn dialog-btn--danger" aria-label=${"Unpair " + name}
          onClick=${() => ask("device:" + d.id, mine ? "Unpair this device?" : "Unpair " + name + "?",
            [mine ? "This window will close, and it will need a new pairing link to come back." : "Any window it has open will close."], "Unpair",
            () => go(revokeCommand(d.id)))}>Unpair</button>
      </div>
      ${here ? renameBox("device", d.id, d.name) : null}
      ${confirmFor("device:" + d.id)}
      ${confirmFor("access:" + d.id)}
    </li>`;
  };
  const devicesBox = () => {
    if (!ro) return html`<p class="set-intro" role="status">Loading…</p>`;
    const n = ro.devices.length;
    const coded = ro.devices.some((d) => deviceCodes(d).length);
    return html`
      ${ro.error ? html`<p class="set-error" role="alert">${ro.error}</p>` : null}
      <section class="remote-sec" aria-labelledby="remote-devices-head">
        <h4 class="set-sub" id="remote-devices-head">${n === 1 ? "1 paired device" : n + " paired devices"}</h4>
        ${n === 0 && !ro.error ? html`<p class="set-intro">Nothing is paired yet.</p>` : null}
        ${coded ? html`<p class="set-about" id="remote-verify-help">${remote ? VERIFY_HELP_RELAY : VERIFY_HELP}</p>` : null}
        ${remote && n ? html`<p class="set-about">${DESK_ONLY_ACCESS}</p>` : null}
        ${n ? html`<ul class="remote-list" aria-label="Paired devices">${ro.devices.map(deviceRow)}</ul>` : null}
      </section>
      ${ro.hosts.length ? hostsBox() : null}`;
  };
  const hostsBox = () => {
    const n = ro.hosts.length;
    return html`<section class="remote-sec" aria-labelledby="remote-hosts-head">
      <h4 class="set-sub" id="remote-hosts-head">${n === 1 ? "1 machine" : n + " machines"}</h4>
      <ul class="remote-list" aria-label="Machines on the account">
        ${ro.hosts.map((h, i) => html`<li class="remote-row" key=${h.id || i} data-host=${h.id}>
          <div class="key-main">
            <div class="key-title"><span class="key-name">${h.name || "Unnamed machine"}</span>${h.self ? html`<span class="key-id">this one</span>` : null}</div>
            <div class="key-status">${h.online ? "online" : "offline — last seen " + ago(h.lastSeen, now)}</div>
          </div>
          ${h.self
            ? html`<div class="key-actions"><button type="button" class="dialog-btn" id="remote-rename" onClick=${() => setRenaming({ kind: "host", id: "", value: h.name })}>Rename</button></div>`
            : null}
          ${h.self && renaming && renaming.kind === "host" ? renameBox("host", "", h.name) : null}
        </li>`)}
      </ul>
      ${ro.hosts.some((h) => !h.self && !h.online)
        ? html`<p class="set-about">A machine that was wiped or lost, and so never turned Flockdeck Remote off, is taken off the account from the Devices page of a paired device.</p>`
        : null}
    </section>`;
  };

  // --- this machine -------------------------------------------------------
  const machineBox = () => {
    const where = bare(r && r.relay, "the relay it is on");
    const retry = r && r.state !== "connected" && !off
      ? html`<button type="button" class="dialog-btn" id="remote-retry" disabled=${!!working} onClick=${() => go({ cmd: "remoteReconnect" }, "reconnect")}>
          ${working === "reconnect" ? "Trying…" : "Try again"}</button>`
      : null;
    if (remote) {
      return html`<section class="remote-sec" aria-labelledby="remote-machine-head">
        <h4 class="set-sub" id="remote-machine-head">This machine</h4>
        <div class="remote-actions">${retry}</div>
        <p class="set-about" id="remote-desk-only">${DESK_ONLY_REMOTE}</p>
        <p class="set-about">${DESK_ONLY_JOIN}</p>
        ${o && o.action === "reconnect" && o.error ? html`<p class="set-error" role="alert">${o.error}</p>` : null}
        <p class="set-intro">${ENTERPRISE_WORDS}</p>
      </section>`;
    }
    const moveForm = () => {
      const field = (key, id, placeholder, label, secret = false) => html`<input type=${secret ? "password" : "text"} class="dialog-field" id=${id} placeholder=${placeholder} aria-label=${label}
        autocomplete="off" spellcheck="false" value=${moving[key]} onInput=${(e) => setMoving({ ...moving, [key]: e.target.value })} onKeyDown=${sayEnter(move)} />`;
      return html`<div class="remote-box" id="remote-move-box">
        <p class="set-about">This machine is enrolled with the new relay first, and leaves ${where} only once the new one answers. Every device paired with it will then have to pair again, with the new relay.</p>
        <div class="remote-form">
          ${field("relay", "remote-move-relay", "New relay, for example relay.example.com", "New relay address")}
          ${field("invite", "remote-move-invite", "Invitation code, if the new relay asks for one", "Invitation code", true)}
          ${field("join", "remote-move-join", "Join code, to join an account already on the new relay", "Join code", true)}
        </div>
        <div class="remote-actions">
          <button type="button" class="dialog-btn dialog-btn--primary" id="remote-move-go" disabled=${!!working} onClick=${move}>${working === "move" ? "Moving…" : "Move this machine"}</button>
        </div>
        ${o && o.action === "move" && o.error ? html`<p class="set-error" role="alert">${o.error}</p>` : null}
        <p class="set-intro">The same from a terminal: flockdeck remote move &lt;relay&gt;.</p>
      </div>`;
    };
    function move() {
      if (working) return;
      const cmd = moveCommand(moving);
      if (!cmd) {
        setProblem("Name the relay to move this machine to.");
        return;
      }
      ask("machine", "Move this machine off " + where + " and onto " + bare(cmd.relay) + "?",
        ["Every device paired with it will have to pair again, with the new relay.", "Only this machine leaves " + where + ": its account there stays."], "Move this machine",
        () => { setProblem(""); if (go(cmd, "move")) { sentCodes.current.move = { join: moving.join, invite: moving.invite, at: Date.now() }; setMoving({ ...moving, invite: "", join: "" }); } });
    }
    return html`<section class="remote-sec" aria-labelledby="remote-machine-head">
      <h4 class="set-sub" id="remote-machine-head">This machine</h4>
      <div class="remote-actions">
        ${retry}
        ${off
          ? html`<button type="button" class="dialog-btn dialog-btn--primary" id="remote-resume" disabled=${!!working} onClick=${() => go(resumeCommand(), "enable")}>
              ${working === "enable" ? "Turning it on…" : "Turn on Flockdeck Remote"}</button>`
          : html`<button type="button" class="dialog-btn" id="remote-disable" disabled=${!!working}
              onClick=${() => ask("machine", "Turn off Flockdeck Remote?",
                ["This machine stops connecting to " + where + ", and your paired devices see it as offline.",
                  "Nothing is deleted: the account, its paired devices and its plan stay, and turning it on again reconnects to the same account with no new sign-up.", KEPT_WORDS],
                "Turn off", () => go({ cmd: "remoteDisable" }, "disable"))}>${working === "disable" ? "Turning it off…" : "Turn off Flockdeck Remote"}</button>`}
        ${off
          ? null
          : html`<button type="button" class="dialog-btn" id="remote-move" disabled=${!!working} aria-expanded=${moving.open ? "true" : "false"}
              onClick=${() => setMoving({ ...moving, open: !moving.open })}>${moving.open ? "Keep it here" : "Move to another relay…"}</button>`}
        <button type="button" class="dialog-btn dialog-btn--danger" id="remote-remove" disabled=${!!working}
          onClick=${() => ask("machine", "Remove this desktop from the account?",
            [where + " is told to delete this machine. The account, its plan, trial, verified email and paired devices stay, even if this is the last machine.",
              "To erase the account, run flockdeck remote delete-account in a terminal. To keep this machine enrolled, choose Turn off Flockdeck Remote instead."],
            "Remove this desktop", () => go({ cmd: "remoteRemove", force: false }, "remove"))}>${working === "remove" ? "Removing…" : "Remove this desktop from the account…"}</button>
      </div>
      ${confirmFor("machine")}
      <p class="set-about">${off ? "Turned off here, this machine shows as offline on your devices. " + KEPT_WORDS : "Turning it off keeps this machine enrolled and the account as it is. " + KEPT_WORDS}</p>
      ${moving.open && !off ? moveForm() : null}
      <div class="remote-actions">
        <button type="button" class="dialog-btn" id="remote-joincode" disabled=${!!working || wanting === "host"} onClick=${() => pairNow("host")}>
          ${wanting === "host" ? "Asking the relay…" : "Show a join code"}</button>
      </div>
      ${join && join.error ? html`<p class="set-error" role="alert">${join.error}</p>` : null}
      ${join && join.code && !joinOk
        ? html`<p class="set-error" id="remote-join-expired" role="status">That join code has expired. Ask for a new one.</p>`
        : null}
      ${join && join.code && joinOk
        ? joinLine((r && r.relay) || "", join.code)
          ? html`<p class="set-about" id="remote-join-text">On the other machine, run <code>${joinLine((r && r.relay) || "", join.code)}</code>. It works once${untilText(join.expiresAt)}. That machine then shares this account, and devices paired with either reach both.</p>`
          : html`<p class="set-error" id="remote-join-bad" role="alert">The relay sent a join code or address that is not plain text, so it is not shown. Do not paste it into a terminal.</p>`
        : null}
      ${o && (o.action !== "enable" || off) && o.action !== "move" && o.error ? html`<p class="set-error" role="alert">${o.error}</p>` : null}
      ${o && o.action === "move" && o.warning ? html`<p class="set-error" role="alert">${o.warning}</p>` : null}
      ${o && o.untold
        ? html`<div id="remote-untold">
            <p class="set-about">Try again first: a relay that cannot be reached is most often the network, for now. Forgetting it here anyway removes this machine from Flockdeck Remote here, but the relay keeps listing it, offline, until it is removed from a paired device's Devices page.</p>
            <div class="remote-actions">
              <button type="button" class="dialog-btn dialog-btn--primary" id="remote-remove-again" disabled=${!!working} onClick=${() => go({ cmd: "remoteRemove", force: false }, "remove")}>Try again</button>
              <button type="button" class="dialog-btn dialog-btn--danger" id="remote-forget" disabled=${!!working}
                onClick=${() => ask("untold", "Forget Flockdeck Remote here without telling the relay?",
                  ["It will keep listing this machine, offline, until it is removed from a paired device."], "Forget it here", () => go({ cmd: "remoteRemove", force: true }, "remove"))}>Forget it here anyway</button>
            </div>
            ${confirmFor("untold")}
          </div>`
        : null}
      <p class="set-intro">${ENTERPRISE_WORDS}</p>
    </section>`;
  };

  const plan = ro && ro.plan;
  return html`
    <h3 class="set-head">Flockdeck Remote</h3>
    ${news ? html`<p class="set-status" role="status" id="remote-news">${news}</p>` : null}
    ${problem ? html`<p class="set-error" role="alert" id="remote-problem">${problem}</p>` : null}
    ${said ? html`<p class="set-status" role="status" id="remote-said">${said}</p>` : null}
    ${notOn
      ? html`<p class="set-intro" id="remote-intro">${INTRO_WORDS}</p>${remote ? html`<p class="set-about" id="remote-desk-only">${DESK_ONLY_REMOTE}</p>` : enableForm()}`
      : html`
        ${r ? html`<div class=${"remote-status " + r.state} id="remote-status" role="status">${summaryOf(r, now)}</div>` : null}
        ${plan
          ? html`<section class="remote-sec" id="remote-plan" aria-labelledby="remote-plan-head">
              <h4 class="set-sub" id="remote-plan-head">Plan</h4>
              <p class=${plan.active ? "" : "set-error"}>${planText(plan, now)}</p>
              <p class="set-about">${PLAN_WORDS}</p>
            </section>`
          : null}
        ${off ? null : pairBox()}
        ${devicesBox()}
        ${machineBox()}`}
  `;
}
