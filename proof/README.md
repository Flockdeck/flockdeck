# popover-s1 staged run: isolation proof

- Built from branch popover-s1; APPDATA, LOCALAPPDATA, USERPROFILE, HOME, TEMP in %TEMP%\flockdeck-s1shots-*; every FLOCKDECK_*/PERCH_* unset in the launching shell and an allow-listed env for every child (cmd/sitegen/shots/guard.mjs).
- `-solo -no-window`, staged instance http://127.0.0.1:58839 (pid 62552), instance.json inside the work dir; staged projects in C:\code-s1 (removed after).
- Live %APPDATA%\flockdeck: 88 files before (live-before.json), after (live-after.json): 0 removed, instance.json unchanged.
  - Changed: layout-1595666930eae949.json, worktree-procs.json: the live instance saving its own state.
  - Added: sessions/6b37e41b-22c4-4ca3-8966-3ccb9f4b7d0f.settings.json: a Claude session settings file whose hook URL is 127.0.0.1:65105, which is the LIVE instance's pane API (listening socket owned by live pid 49176, UI on :65107); its session id is in the live layout file. A pane opened in the live window during the run, not by this run (the staged instance was on :58839 and its mock agents register no hooks).
- "nothing of the harness's is running" at the end; checks.json: keyboard, reduced motion, and `offOrigin: []` (no request left the origin).
