// A fake of claude's AskUserQuestion screen: it does what the keys were captured doing in
// claude 2.1.325 (docs/terminal-questions.md) and draws the rows the same way.

export class FakeTui {
  constructor(qs) {
    this.qs = qs;
    this.at = 0;
    this.cursor = 1;
    this.picked = qs.map(() => null); // single: row number; multi: Set of row numbers
    this.text = qs.map(() => "");
    this.sent = [];
    this.submitted = false;
    this.cancelled = false;
    this.qs.forEach((q, i) => { if (q.multiSelect) this.picked[i] = new Set(); });
    // A single-choice question alone has a lone header and no review; Enter on a row submits.
    this.one = qs.length === 1 && !qs[0].multiSelect;
    this.footer = "Enter to select · Tab/Arrow keys to navigate · Esc to cancel";
    this.cols = 110;
    this.drift = null; // a function that changes the rows drawn, for a claude that draws differently
    this.onSend = null; // called with the keys, before they are pressed
  }
  get review() { return this.at >= this.qs.length; }
  get q() { return this.qs[this.at]; }
  get other() { return this.q.options.length + 1; }
  rowCount() { return this.q.options.length + 2; }
  next() { this.at++; this.cursor = 1; }
  press(tok) {
    if (this.submitted || this.cancelled) return;
    if (this.review) {
      if (tok === "down") this.cursor = Math.min(2, this.cursor + 1);
      else if (tok === "up") this.cursor = Math.max(1, this.cursor - 1);
      else if (tok === "enter") { if (this.cursor === 1) this.submitted = true; else this.cancelled = true; }
      else if (tok === "left") { this.at = this.qs.length - 1; this.cursor = 1; }
      return;
    }
    const q = this.q;
    const onText = this.cursor === this.other;
    if (tok === "down") this.cursor = Math.min(this.rowCount(), this.cursor + 1);
    else if (tok === "up") this.cursor = Math.max(1, this.cursor - 1);
    else if (tok === "left") { if (this.at > 0) { this.at--; this.cursor = 1; } }
    else if (tok === "right") { if (!onText) this.next(); }
    else if (tok === "space" && onText) { this.text[this.at] += " "; if (q.multiSelect) this.picked[this.at].add(this.other); }
    else if (tok === "space" && q.multiSelect) {
      if (this.cursor <= q.options.length) { const s = this.picked[this.at]; s.has(this.cursor) ? s.delete(this.cursor) : s.add(this.cursor); }
    } else if (tok === "enter") {
      if (q.multiSelect) {
        if (this.cursor <= q.options.length) { const s = this.picked[this.at]; s.has(this.cursor) ? s.delete(this.cursor) : s.add(this.cursor); }
      } else if (this.cursor <= q.options.length || (onText && this.text[this.at])) {
        this.picked[this.at] = this.cursor;
        if (this.one) { this.submitted = true; return; }
        this.next();
      }
    } else if (tok.length === 1 && tok >= " ") {
      if (onText) {
        this.text[this.at] += tok;
        if (q.multiSelect) this.picked[this.at].add(this.other);
      } else if (/\d/.test(tok)) {
        const n = Number(tok);
        if (n <= this.rowCount()) {
          if (q.multiSelect) { if (n <= q.options.length) { const s = this.picked[this.at]; s.has(n) ? s.delete(n) : s.add(n); } }
          else this.cursor = n;
        }
      }
    }
  }
  send(keys) {
    this.sent.push(keys);
    if (this.onSend) this.onSend(keys, this);
    const map = { "\x1b[B": "down", "\x1b[A": "up", "\x1b[C": "right", "\x1b[D": "left", "\r": "enter", " ": "space" };
    for (let i = 0; i < keys.length;) {
      const three = keys.slice(i, i + 3);
      if (map[three]) { this.press(map[three]); i += 3; } else if (map[keys[i]]) { this.press(map[keys[i]]); i++; } else { this.press(keys[i]); i++; }
    }
    return true;
  }
  answerOf(i) {
    const q = this.qs[i];
    const p = this.picked[i];
    const label = (n) => (n === q.options.length + 1 ? this.text[i] : q.options[n - 1].label);
    if (q.multiSelect) return [...p].sort((a, b) => a - b).map(label).join(", ");
    return p == null ? "" : label(p);
  }
  lines() {
    const rows = this.draw();
    return this.drift ? this.drift(rows) : rows;
  }
  draw() {
    if (this.submitted && this.one) return ["● User answered Claude's questions:", "  ⎿  · " + this.qs[0].question + " → " + this.answerOf(0)];
    const done = (i) => (this.qs[i].multiSelect ? this.picked[i].size > 0 : this.picked[i] != null);
    const bar = this.one ? " ☐ " + (this.qs[0].header || "") : "←  " + this.qs.map((q, i) => (done(i) ? "☒ " : "☐ ") + (q.header || "")).join("  ") + "  ✔ Submit  →";
    if (this.review) {
      const out = [bar, "", "Review your answers", ""];
      this.qs.forEach((q, i) => out.push(" ● " + q.question, "   → " + this.answerOf(i)));
      out.push("", "Ready to submit your answers?", "", (this.cursor === 1 ? "❯" : " ") + " 1. Submit answers", (this.cursor === 2 ? "❯" : " ") + " 2. Cancel");
      return out;
    }
    const q = this.q;
    const out = [bar, "", q.question, ""];
    q.options.forEach((o, i) => {
      const n = i + 1;
      const mark = q.multiSelect ? (this.picked[this.at].has(n) ? "[✔] " : "[ ] ") : "";
      const tick = !q.multiSelect && this.picked[this.at] === n ? " ✔" : "";
      out.push((this.cursor === n ? "❯" : " ") + " " + n + ". " + mark + o.label + tick);
      if (o.description) out.push("     " + o.description);
    });
    const n = this.other;
    const t = this.text[this.at];
    const label = t || (q.multiSelect ? "Type something" : "Type something.");
    const mark = q.multiSelect ? (this.picked[this.at].has(n) ? "[✔] " : "[ ] ") : "";
    out.push((this.cursor === n ? "❯" : " ") + " " + n + ". " + mark + label);
    if (q.multiSelect) out.push("     Next");
    out.push("─".repeat(100), "  " + (n + 1) + ". Chat about this", "", this.footer);
    return out;
  }
  io() {
    return { screen: () => this.lines(), cols: () => this.cols, send: async (k) => this.send(k), wait: async () => {} };
  }
}

