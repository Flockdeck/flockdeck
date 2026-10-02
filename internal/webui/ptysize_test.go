package webui

import "testing"

// TestTerminalIsKeptTheSizeOfThePanesPty covers a pane shown in two windows.
// There is one pty behind it, and it is the size of the window somebody is
// using; this window's box is a different size, and the terminal fitted to it
// drew the program's cell-addressed output into the wrong cells -- letters
// dropped or swapped, rows on top of each other. The server says what size the
// pty is, and the terminal is kept that size whatever the box allows.
func TestTerminalIsKeptTheSizeOfThePanesPty(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const sock = h.sockets.filter((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"))[0];
assert.ok(sock.url.includes("size=1"), "the window did not ask to be told the pty's size: " + sock.url);
const term = h.terms.find((x) => !x.disposed);

sock.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, resumed: false }) });
sock.onmessage({ data: JSON.stringify({ size: { cols: 132, rows: 41 } }) });
term.parse();
assert.strictEqual(term.cols, 132, "the terminal was not made as wide as the pty");
assert.strictEqual(term.rows, 41, "the terminal was not made as tall as the pty");

// A size is not the opening of a run: it must not start the terminal afresh.
assert.ok(!term.screen.slice(1).includes("\x1bc"), "a size frame cleared the screen");

// The box is still measured and reported -- it is how the server learns what
// this window could show -- but it does not take the terminal off the pty's size.
sock.sent.length = 0;
h.observers.forEach((o) => o.fn());
await h.sleep(120);
term.parse();
assert.strictEqual(term.cols, 132, "fitting the box took the terminal off the pty's size");
assert.strictEqual(term.rows, 41, "fitting the box took the terminal off the pty's size");

// The pty changing size, because the window in use was resized, is followed.
sock.onmessage({ data: JSON.stringify({ size: { cols: 90, rows: 30 } }) });
term.parse();
assert.strictEqual(term.cols, 90);
assert.strictEqual(term.rows, 30);
`)
}

// TestSizeFrameIsDrawnInOrderWithTheOutput is the resize landing where it falls
// in the stream: output ahead of it was drawn for the old size.
func TestSizeFrameIsDrawnInOrderWithTheOutput(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const sock = h.sockets.filter((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"))[0];
const term = h.terms.find((x) => !x.disposed);
sock.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, resumed: false }) });
sock.onmessage({ data: new TextEncoder().encode("before").buffer });
sock.onmessage({ data: JSON.stringify({ size: { cols: 100, rows: 30 } }) });
assert.notStrictEqual(term.cols, 100, "the terminal was resized ahead of the output the server sent before the size");
term.parse();
assert.strictEqual(term.cols, 100, "the terminal was never resized");
`)
}

// TestACroppedTerminalSaysSoAndCanTakeThePaneOver covers the window that is
// not the one the pane is sized for. Its terminal is the pty's size, so a
// smaller box shows only part of it: that has to be said, with the way to have
// the pane fitted to this window, and the bottom -- an agent's prompt and
// status -- is the part kept.
func TestACroppedTerminalSaysSoAndCanTakeThePaneOver(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const sock = h.sockets.filter((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"))[0];
const term = h.terms.find((x) => !x.disposed);
const note = h.$("workspace").querySelector(".size-note");
assert.ok(note, "a pane has nowhere to say it is cropped");
const host = term.host;
await h.sleep(80);
assert.ok(note.hidden, "a pane no size has been said for claims to be cropped");

// The fake box measures 80x24; the pty is larger.
sock.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, resumed: false }) });
sock.onmessage({ data: JSON.stringify({ size: { cols: 132, rows: 41 } }) });
term.parse();
assert.ok(!note.hidden, "a terminal larger than its box did not say it was cropped");
assert.ok(note.textContent.includes("132") && note.textContent.includes("41"), "the note does not say what is being viewed: " + note.textContent);
assert.ok(host.classList.contains("cropped"), "a terminal taller than its box is not kept to the bottom");

// Asking for the pane to be fitted to this window says what the box is, and
// that this window is the one in use.
sock.sent.length = 0;
note.onclick({ stopPropagation() {} });
const sent = sock.sent.map((m) => (typeof m === "string" ? m : ""));
assert.ok(sent.some((m) => m.includes('"resize"') && m.includes('"cols":80')), "taking over did not say what size this window is: " + sent);
assert.ok(sent.some((m) => m.includes('"focus"')), "taking over did not say this window is the one in use: " + sent);

// Once the server has made the pane that size the note goes.
sock.onmessage({ data: JSON.stringify({ size: { cols: 80, rows: 24 } }) });
term.parse();
assert.ok(note.hidden, "the note stayed after the pane was fitted to this window");
assert.ok(!host.classList.contains("cropped"));
`)
}

// TestAPaneIsNeverMadeLargerThanAScreenShows holds the page to the bound the
// server holds a window to: a size past it is taken as the most, and one that
// is no size for a terminal is ignored.
func TestAPaneIsNeverMadeLargerThanAScreenShows(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.open();
const sock = h.sockets.filter((s) => s.url.includes("/ws/pty") && s.url.includes("id=p1"))[0];
const term = h.terms.find((x) => !x.disposed);
sock.onmessage({ data: JSON.stringify({ epoch: 1, offset: 0, resumed: false }) });
sock.onmessage({ data: JSON.stringify({ size: { cols: 2000, rows: 2000 } }) });
term.parse();
assert.strictEqual(term.cols, 500);
assert.strictEqual(term.rows, 200);
sock.onmessage({ data: JSON.stringify({ size: { cols: 1, rows: 1 } }) });
term.parse();
assert.strictEqual(term.cols, 500, "a size too small for a terminal was taken");
`)
}
