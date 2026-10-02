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
