// A fake WebSocket and a fake clock, so the socket client and the store run
// without a network or a real wait.

export class FakeWebSocket {
  static instances = [];
  static reset() { FakeWebSocket.instances = []; }
  static get last() { return FakeWebSocket.instances[FakeWebSocket.instances.length - 1]; }

  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.sent = [];
    this.closed = false;
    FakeWebSocket.instances.push(this);
  }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() {
    if (this.closed) return;
    this.closed = true;
    this.readyState = 3;
    if (this.onclose) this.onclose({});
  }
  // The server's side of the socket.
  open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
  frame(obj) { this.raw(JSON.stringify(obj)); }
  raw(data) { if (this.onmessage) this.onmessage({ data }); }
  drop() { this.readyState = 3; if (this.onclose) this.onclose({}); }
  commands(name) { return this.sent.filter((c) => c.cmd === name); }
}

export class FakeClock {
  constructor() { this.now = 0; this.timers = new Map(); this.next = 1; }
  setTimeout = (fn, ms) => {
    const id = this.next++;
    this.timers.set(id, { at: this.now + ms, fn });
    return id;
  };
  clearTimeout = (id) => { this.timers.delete(id); };
  // advance runs every timer due within ms, in order.
  advance(ms) {
    const end = this.now + ms;
    for (;;) {
      let pick = null;
      for (const [id, t] of this.timers) {
        if (t.at <= end && (!pick || t.at < pick.t.at)) pick = { id, t };
      }
      if (!pick) break;
      this.timers.delete(pick.id);
      this.now = pick.t.at;
      pick.t.fn();
    }
    this.now = end;
  }
  get pending() { return this.timers.size; }
}

// harness builds a store on a fake socket and clock.
export async function harness(extra = {}) {
  const { createStore } = await import("../assets/lib/store.js");
  FakeWebSocket.reset();
  const clock = new FakeClock();
  const store = createStore({
    url: "ws-test", WebSocket: FakeWebSocket,
    setTimeout: clock.setTimeout, clearTimeout: clock.clearTimeout, now: () => clock.now, ...extra,
  });
  return { store, clock };
}

export const hello = (over = {}) => ({ type: "hello", keys: [], prefs: {}, ...over });
