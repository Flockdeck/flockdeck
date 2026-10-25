# ui

The browser front end and what builds and tests it. It is its own Go module (`github.com/jmwri/flockdeck/ui`); the server module uses it and it imports nothing of the server (`cmd/boundary` checks that).

- `src/`: the page, as ES modules, plus `keys.json` and `help.json`, which `help/gen` writes.
- `help/`: the help pages and the key table, and the generator.
- `build/`: the joiner that sends the modules and stylesheets as one file each.
- `test/`: node tests. `go test ./...` in this folder runs them, with the content-policy tests.

## Working on the front end without a build

Set `FLOCKDECK_UI_DIR` to the `src` folder and start Flockdeck. The server serves the page and its files from that folder, as they are (not joined), and reads them on each request, so a reload shows an edit. It logs the folder at start. A folder with no `index.html` is ignored, with a log line saying so. Quotes around the value and spaces at either end are taken off (cmd.exe keeps both in `set X="C:\my files"`), and if the folder has a `src` with an `index.html` in it the log line says so.

Windows reached through the relay are always served the files built into the program, so the folder never goes to a phone and the relay's compressed copies stay those of the built-in files.

The folder is read with `os.DirFS`, which follows symlinks inside it, so a link in the folder to a file elsewhere is served. That is fine for a folder you chose to develop in and is why this is not for anything else.

`FLOCKDECK_UNBUNDLED` (any value) serves the built-in files unjoined.
