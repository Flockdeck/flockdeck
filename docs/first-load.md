# First load of the shell

This is step one of T77 (S6): how long the new shell takes to show on a phone's link, from a local run behind a throttling proxy. Step two, the same page through the real relay on a paired phone, is not done and needs the user.

The budget is 3 seconds, for both numbers below.

## Result

Measured at `dcc0554b` (main after #394), five runs per case, median. Times are milliseconds from the start of the navigation. The browser is Chrome 153 in headless mode, with the page at a 390 by 844 phone size and the CPU slowed 4 times. The link is 1.6 Mbit/s shared by all requests, with a 150 ms round trip. A request costs 3 round trips before its first byte: TCP, TLS and the request. HTTP/1.1, one connection per request, 6 requests in flight, and TCP slow start. Text files are gzipped at the best level, as the server does for a relay request.

| Case | Requests | Bytes sent | First meaningful paint | Interactive |
| --- | --- | --- | --- | --- |
| Modules as shipped | 17 | 200 KB | 3214 | 3313 |
| Concatenated | 8 | 196 KB | 2255 | 2349 |
| Modules, finished app | 24 | 223 KB | 3715 | 3811 |
| Concatenated, finished app | 8 | 208 KB | 2359 | 2458 |
| Modules, no terminal libraries | 13 | 69 KB | 2038 | 2135 |
| Concatenated, no terminal libraries | 4 | 65 KB | 1103 | 1203 |
| Modules as shipped, no gzip | 17 | 624 KB | 6507 | 6601 |
| Concatenated, no gzip | 8 | 623 KB | 5583 | 5682 |

First meaningful paint is the first time the app's own markup is inserted into the page, which is before the browser paints it. The script also records the first contentful paint, which comes 100 to 140 ms later in every case (3324 against 3214 for the modules as shipped, 2360 against 2255 concatenated). Interactive is measured from the same point. Interactive is the later of first meaningful paint, the end of the document's scripts and the last long task. The page's load event comes about 0.8 s later in every case, because the font is the last file to arrive; it is not counted.

"Finished app" is a stand-in: the shell's entry plus every library module that nothing imports yet (store, socket, pty, terminal, use-store, the graph view model). It is there to show what the module count does once those are in use, not a prediction of the final size.

## What it says

- The shell as shipped is 7 to 10 percent over the budget (3.2 s to first paint, 3.3 s interactive), which is near it. The finished-app figures are for a stand-in that imports modules nothing uses yet, not for a real app. With the stand-in the modules case is over by about 0.7 s (24 percent).
- Concatenating the same code into one file saves about 1 s now and 1.35 s with the finished app, and is under the budget in both. The bytes sent barely change; the saving is round trips. With 3 round trips per request and a cap of 6 streams, the 17 or 24 files go out in waves, and the module graph also finds its files one level at a time.
- The terminal libraries are the larger cost. xterm.js, its fit addon and the end-to-end layer are three classic scripts that stop the parser until they have arrived: 131 KB of the 200 KB, and about 1.1 s in both the module and the concatenated case. Loading them when a terminal is first opened, not at page load, would save that without a build step. That was done in T99; see the section after the result.
- Without gzip the page is 6.5 s. The relay path compresses (`authFiles`), so this is the figure for any path that does not.

## After the terminal libraries moved (T99)

The three scripts are no longer in `index.html`. The first terminal that opens loads them (`lib/load-script.js`: xterm, the fit addon, then e2e.js), and a window reached through the relay loads e2e.js when its hello arrives (`lib/e2e-boot.js`). Both share one load of e2e.js. The same tool, same settings and a fresh run of five, on main after #399. The eager rows are the old page, rebuilt by putting the three scripts back in front of the module; they match the earlier table within about 1 percent.

| Case | Requests | Bytes sent | First meaningful paint | Interactive |
| --- | --- | --- | --- | --- |
| Modules, libraries on first use | 15 | 72 KB | 2055 | 2152 |
| Modules, libraries in the page (before) | 18 | 201 KB | 3197 | 3278 |
| Concatenated, libraries on first use | 6 | 68 KB | 1115 | 1204 |
| Concatenated, libraries in the page (before) | 9 | 197 KB | 2242 | 2328 |
| Modules, finished app, on first use | 23 | 98 KB | 2591 | 2677 |
| Modules, finished app, in the page (before) | 26 | 226 KB | 3710 | 3800 |
| Concatenated, finished app, on first use | 6 | 81 KB | 1221 | 1304 |
| Concatenated, finished app, in the page (before) | 9 | 209 KB | 2333 | 2418 |

First paint falls by 1142 ms for the modules as shipped and by 1127 ms concatenated. The module graph is now under the 3 s budget on its own, including the finished-app stand-in (2.6 s), so the case for a concatenation tool is weaker than the first table made it. It is still 1.4 s faster, and the module graph is the case that grows with the stream cap, so step two on a phone still decides.

The cost moves to the first terminal: it now waits for 131 KB (gzipped) fetched as three requests at once, which run in order. By arithmetic from the link (a first byte after 3 round trips, then 131 KB at 200 KB/s) that is about 1.1 to 1.3 s, with "Loading terminal" showing meanwhile; it was not measured, and the table measures the page before any terminal opens. A terminal waits for e2e.js as well as xterm, so a failed e2e.js load stops a local terminal too: that is the strict choice, since the hello that says whether the window is remote may not have arrived when the load starts.

## Measured again when the old window was deleted (T162)

Same tool and settings (five runs, median), on the shell as it is after the old window's deletion, branched from `0ecb8517`. The browser is Chrome 153 headless. Esbuild was not installed, so only the module cases ran; no concatenated figure was taken.

| Case | Requests | Bytes sent | First meaningful paint | Interactive |
| --- | --- | --- | --- | --- |
| Modules as shipped | 105 | 326 KB | 9936 | 10026 |
| Modules, finished-app stand-in | 106 | 326 KB | 9665 | 9766 |
| Modules, libraries in the page (before T99) | 108 | 455 KB | 11179 | 11294 |
| Modules as shipped, no gzip | 105 | 859 KB | 11665 | 12510 |

The shell has grown from 17 files and 200 KB to 105 files and 326 KB, and on this model it shows in about 10 seconds against the 3 second budget. The cost is the round trips: 105 files go out in waves of six, each file waiting three round trips for its first byte, and a module graph finds its imports one level at a time. The finished-app stand-in no longer adds anything, since the shipped page already imports nearly everything. The earlier tables above are the history of the first, smaller page and are not comparable with this one.

What this does not show: a real phone, the real relay's stream cap (still a guess), or how much a concatenated file would save now. The previous measurement put concatenation at about a third off; with six times the files the saving should be larger, but that is an expectation and not a number. The decision on a concatenation tool in the release build is still open and is now the main first-load item.

## Sensitivity

Median first paint for the modules and the concatenated case, other settings as above, three runs:

| Setting | Modules | Concatenated |
| --- | --- | --- |
| 6 streams (above) | 3214 | 2255 |
| 4 streams | 3590 | 2274 |
| 2 streams | 4988 | 2739 |
| No slow start | 3223 | 2282 |
| CPU not slowed | 3063 | 2120 |

The module graph is sensitive to how many streams the relay carries at once; the concatenated page hardly is. The stream cap used here is a guess, since nothing in this repository says what the relay allows. Step two should settle it.

## Limits of this measurement

- The proxy models bandwidth, round trips, the stream cap and slow start. It does not model loss, bursts, TLS compute time or the relay's own queueing.
- Headless Chrome on a development machine with a 4 times CPU slowdown is a guess at a mid-range phone, not one.
- The page has no server behind it here, so the control socket is not opened. The shell as shipped does not open one either. The numbers are for the files and the first render only, not for the time until the graph shows.
- Concatenation here is done by esbuild 0.25.0, as a throwaway, with `--bundle --format=esm --tree-shaking=false` and no minification. The concatenated numbers depend on both: a minified or tree-shaken bundle would be smaller. The tool records the esbuild version in its `--out` JSON. It is not a proposal for the release tool.

## Decision

Left to the manager. When this was first measured the shell as shipped was near the budget (7 to 10 percent over) and the stand-in for the finished app 24 percent over, which leaned towards the follow-up task for a small Go concatenation tool, run for release builds only. With the terminal libraries moved out (T99) both are under 3 s without it. Step two on a real phone would confirm the stream cap and the round-trip cost before deciding.

## Running it

```
npm install esbuild --prefix <scratch>
node scripts/firstload/measure.mjs --esbuild <scratch>/node_modules/@esbuild/win32-x64/esbuild.exe --runs 5
```

The esbuild path above is the Windows x64 build; use the matching `@esbuild/<platform>` package elsewhere. The browser search in `findBrowser` lists mostly Windows paths (a Playwright Chromium, Chrome, Edge) and a few Linux and macOS ones, so on another system pass `--browser`.

The cases are the module graph and the concatenated one, each for the shipped page, for the finished-app stand-in, and for the old page with the three libraries in it (`-eager`), which the first table's figures came from before T99. Without `--esbuild` only the module cases run. The tool needs Node 22 or later and a Chromium (`--browser` names one). Options for the link speed, round trip, setup round trips, stream cap and CPU slowdown are listed at the top of the script. It reads only the source tree, writes only to a temporary folder that it removes at the end, and does not start Flockdeck.
