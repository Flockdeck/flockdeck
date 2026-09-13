package webui

import "testing"

// A terminal socket that drops and comes back: what the page asks for, and what
// it does with the answer. The server's half is held to account in
// internal/server; these hold the page to its side of it.

// TestTerminalResumesWhereItWasCutOff covers a socket that comes back after
// the pane had printed. The page says how much of the pane's output it holds,
// and a stream that carries on from there must not wipe the screen it kept.
func TestTerminalResumesWhereItWasCutOff(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const ptys = () => h.sockets.filter((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"));
const first = ptys()[0];
assert.ok(first.url.includes("from=-1"), "a first connection did not ask for everything: " + first.url);

first.onmessage({ data: JSON.stringify({ epoch: 7, offset: 100, resumed: false }) });
first.onmessage({ data: new TextEncoder().encode("hello").buffer });
const term = h.terms.find((t) => t.written.length === 1);
assert.ok(term, "the pane's terminal was not written to");

first.close();
await h.sleep(400);
const second = ptys().pop();
assert.ok(second !== first, "the terminal socket was not opened again");
assert.ok(second.url.includes("epoch=7") && second.url.includes("from=105"),
  "the reconnect did not say how much it holds: " + second.url);
h.open();
second.onmessage({ data: JSON.stringify({ epoch: 7, offset: 105, resumed: true }) });
assert.strictEqual(term.written.length, 1, "a resumed stream wiped the screen it carries on from");
`)
}

// TestTerminalThatReceivedNothingStartsAgain covers a stream cut off between its
// header and its first bytes. The header's place counts the terminal modes put
// back ahead of the replay, which the page never received, so resuming from it
// would carry on without them.
func TestTerminalThatReceivedNothingStartsAgain(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const ptys = () => h.sockets.filter((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"));
const first = ptys()[0];
first.onmessage({ data: JSON.stringify({ epoch: 9, offset: 40, resumed: false }) });
first.close();
await h.sleep(400);
const second = ptys().pop();
assert.ok(second !== first, "the terminal socket was not opened again");
assert.ok(second.url.includes("from=-1"),
  "a stream that had delivered nothing was resumed from inside it: " + second.url);
`)
}

// TestTappingATerminalClaimsThePane covers the focus frame. A pane watched from
// several windows is sized for the one in use, and tapping its terminal is
// using it.
func TestTappingATerminalClaimsThePane(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
for (const t of h.terms) h.dispatch(t.host, new h.Ev("pointerdown", { target: t.host }));
const sock = h.sockets.find((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"));
assert.ok(sock.sent.includes(JSON.stringify({ focus: true })),
  "tapping the terminal did not tell the server it is in use: " + JSON.stringify(sock.sent));
`)
}

// TestAFreshStartClearsWhatWasStillBeingDrawn covers a run that starts afresh
// while the terminal is still behind: a restart on the same socket, or a slow
// window dropped and reconnected. xterm's reset() acts at once and write()
// only queues, so the old run's bytes still queued were drawn after the reset,
// on the screen that was meant to start empty.
func TestAFreshStartClearsWhatWasStillBeingDrawn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const sock = h.sockets.find((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"));
const bytes = (s) => new TextEncoder().encode(s).buffer;
sock.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, resumed: false }) });
sock.onmessage({ data: bytes("the old run") });
const term = h.terms.find((t) => t.written.length === 1);
assert.ok(term, "the pane's terminal was not written to");

// The pane restarts before the terminal has parsed a byte of the old run.
sock.onmessage({ data: JSON.stringify({ epoch: 2, offset: 0, resumed: false }) });
sock.onmessage({ data: bytes("the new run") });
const shown = term.written.map((d) => (typeof d === "string" ? d : new TextDecoder().decode(d))).join("");
assert.strictEqual(shown, "the new run", "the old run was drawn on the fresh screen");
`)
}

// TestTheAnswersToAReplayAreNotTyped covers the history a terminal is sent when
// it connects. The terminal answers the questions in it - what it is, where the
// cursor is, what colour the background is - as though they had just been
// asked, and every answer went to the program as typing, on every attach.
func TestTheAnswersToAReplayAreNotTyped(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const sock = h.sockets.find((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"));
const replay = new TextEncoder().encode("\x1b[c\x1b[6n\x1b]11;?\x07history");
sock.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, resumed: false, end: replay.length }) });
sock.onmessage({ data: replay.buffer });
const term = h.terms.find((t) => t.written.length === 1);
assert.ok(term, "the pane's terminal was not written to");
const typed = () => sock.sent.filter((d) => typeof d !== "string").map((b) => new TextDecoder().decode(b));

for (const answer of ["\x1b[?1;2c", "\x1b[>0;276;0c", "\x1b[3;1R", "\x1b[0n", "\x1b[?2004;1$y",
                      "\x1b]11;rgb:0f0f/1111/1414\x1b\\", "\x1bP1$r0m\x1b\\", "\x1b[I", "\x1b[O"]) {
  term._data(answer);
}
assert.deepStrictEqual(typed(), [], "the terminal's answers to the replay were sent as typing");
term._data("y");
term._data("\x1b[A");
assert.deepStrictEqual(typed(), ["y", "\x1b[A"], "typing while the replay was drawn was kept from the program");

// Once the replay has been drawn, the terminal answers the program live.
term.parse();
term._data("\x1b[?1;2c");
assert.strictEqual(typed().pop(), "\x1b[?1;2c", "an answer to a live question was kept from the program");
`)
}
