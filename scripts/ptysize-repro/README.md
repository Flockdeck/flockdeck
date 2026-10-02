# Garbling when a terminal is not the size of its pty

Shows what a window draws when its xterm.js is a different size from the pty a
program (and ConPTY, which repaints with only the cells that changed) drew for.
It runs a child under a real pty, captures what the pty emits, and replays that
into xterm.js at the pty's size and at another. It never starts Flockdeck or
touches its config.

    go build -o child.exe scripts/ptysize-repro/child.go
    go build -o host.exe  scripts/ptysize-repro/host.go
    ./host.exe ./child.exe 110 25 out.bin        # capture at 110x25
    npm i @xterm/headless @xterm/addon-unicode11
    node scripts/ptysize-repro/replay.js out.bin 110 25 1   # clean
    node scripts/ptysize-repro/replay.js out.bin 90 25 1    # old status lines pile up on the prompt row

(`host.go` sets `GARBLE_COPY` so the child also writes what it meant to print,
to `out.bin.direct`.)
