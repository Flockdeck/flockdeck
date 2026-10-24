# Writing tests that stay isolated

A test must never read the config directory of the person running it, use a
key they have stored, or reach a real host. Flockdeck keeps `agents.json`,
`keys.json` (including the stored TypeSafe/Jev key) and the rest of its state
under `os.UserConfigDir()`, so a test that resolves it on a developer's machine
reads their real files, and a stored key could send a test's fixture text to
the real API.

## What the guard does

Every package whose tests can reach the config directory, a key or `net/http`
has an `isolation_test.go`:

```go
func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }
```

`internal/testiso` then, before any test runs:

- points `APPDATA`, `LOCALAPPDATA`, `XDG_CONFIG_HOME`, `HOME` and `USERPROFILE` at a fresh
  temporary directory, so `os.UserConfigDir` and `os.UserHomeDir` (and so
  `store.Dir`) give a throwaway place on Windows, macOS and Linux, and unsets
  `HOMEDRIVE` and `HOMEPATH`. It also unsets `CLAUDE_CONFIG_DIR` and `CODEX_HOME`,
  which the Claude Code and Codex readers look at before `HOME`;
- unsets every `*_API_KEY`, `*_API_TOKEN` and `*_AUTH_TOKEN` in the environment;
- unsets every `FLOCKDECK_*` and `PERCH_*` variable (the pane identity, the relay,
  the update switch, the signing key and the rest) and `GIT_CONFIG_COUNT`, except
  the `FLOCKDECK_TEST_*` and `FLOCKDECK_ACPTEST_*` helpers and the switches in
  `keptEnv` in `internal/testiso/iso/iso.go` that a person or CI sets to steer
  the tests (`FLOCKDECK_LIVE_AGENT_TESTS`, `FLOCKDECK_UPDATE_GOLDEN` and so on).
  A test that reads another `FLOCKDECK_` variable from outside adds it there; a
  test in `iso` fails until it does;
- sets `GOCACHE` and `GOMODCACHE`, where they are unset, to where the go command
  would have put them before the redirect, so a test that runs `go build` does
  not compile everything into an empty cache;
- removes `flockdeck-test-home-*`, `flockdeck-state-*` and `flockdeck-wait-*` directories in the system temp directory that
  are over a day old, which a run that was killed leaves behind;
- puts a stub of each agent the catalog launches by name (`claude`, `codex`,
  `gemini`, `aider`, `opencode`, `cursor-agent`) first on `PATH`, so a test that
  opens a pane starts a program that only idles, never the real agent installed
  on the machine. Set `ISO_STUB_LOG` to a file to have each stub record its
  arguments, directory and home. The stubs are left out when
  `FLOCKDECK_LIVE_AGENT_TESTS` is set, for the live tests. A test checks the
  list against the catalog, so a new agent needs a name in
  `iso.StubNames`;
- refuses any request or dial to a host that is not loopback, through
  `http.DefaultTransport` (so `http.DefaultClient` and any client without a
  `Transport` of its own);
- makes `store.Dir` panic if a test points the config directory back at the real
  one.

A refusal is also remembered: if a test swallows the error, the run still exits
non-zero and lists what was refused.

`internal/store` cannot import `testiso` (it would be a cycle) and uses
`internal/testiso/iso` directly; see `internal/store/isolation_test.go`.

## Rules for new tests

- **New package?** Add an `isolation_test.go` like the one above. If the package
  already has a `TestMain`, end it with `os.Exit(testiso.Main(m))` in place of
  `os.Exit(m.Run())`.
- **Talk to an in-process fake.** Use `httptest.NewServer` and give the code
  under test its URL (`jev.Client{APIBase: srv.URL}` and the like). Never a real
  hostname, not even one you expect to fail.
- **A client with its own `Transport`, or a raw `net.Dial`, is not covered.**
  Build such a client against a loopback address, and do not rely on the guard
  to catch it.
- **A command that checks a key against the vendor** (`keys set` at a terminal,
  `keys check`) goes to the real API unless the agent's endpoint is pointed at a
  fake first; see `fakeKeyEndpoint` in `cli_keys_test.go`.
- **Need a key?** Set it with `t.Setenv("TYPESAFE_API_KEY", "k-test")`. An
  *empty* value counts as unset and falls through to the stored key, which in an
  isolated run is simply absent.
- **Need files in the config directory?** `store.Dir()` is already temporary.
  Write there, or call `t.Setenv("APPDATA", dir)` and `t.Setenv("XDG_CONFIG_HOME",
  dir)` with `t.TempDir()` for one test; never a path under the real home.
- **Spawning the built binary?** A child inherits the redirected environment, so
  pass `os.Environ()` along (a test binary re-run as a child keeps the
  environment it is given and still remembers which directory is the real one), or build the environment by hand, adding `iso.ChildEnv()`, and set `APPDATA`, `LOCALAPPDATA`, `XDG_CONFIG_HOME`
  and `HOME`/`USERPROFILE` explicitly as `quit_agent_env_test.go` does.
- **Running the test binary again as a fake program?** Call `depth.Guard()`
  (`internal/testiso/depth`) first in the `TestMain` or `init` that does it,
  unless `testiso.Main` or `acptest.RunIfAgent` already runs first. If the
  variable or argument that says what the child is gets lost, the child runs
  the whole suite and starts the next one. The guard counts ancestors running
  the same program and exits with status 2 at three, which no suite needs. It
  fails open: where the process table cannot be read (an unsupported system, or
  a failed snapshot or `/proc` read) it does nothing, so it is a second line of
  defence and does not replace passing the child what it needs. A test in
  `internal/testiso/depth` walks every `*_test.go` in the repository (the root
  package, `cmd/` and `internal/`; not `.git`, `node_modules`, `testdata` or
  `vendor`) and fails if a package's tests run their own binary
  (`os.Executable()`, `os.Args[0]` or `acptest.Command`) and none of its test
  files calls `depth.Guard`, `iso.Main`, `testiso.Main` or `acptest.RunIfAgent`.
  It checks that a call is there, not that it comes first.

If a guard trips, the message names what was reached; fix the test rather than
the guard. `internal/testiso/testiso_test.go` holds the regression tests that
prove each guard trips.

## What a pull request runs

The test workflow (`.github/workflows/test.yml`) runs the whole suite on Linux,
Windows and macOS for each pull request and each push to main, with the race
detector on Linux (and on macOS for pushes to main), and the purge script's
tests in a job of their own.

The suite is split into four shares per platform that run at the same time, and
so is the race detector. Each job works out the same split from the names of the
packages and tests (`.github/scripts/shardtests.go`): a package is in one share,
and the slowest packages are cut by test name, so a new test is placed by its
name and nothing is listed anywhere. `.github/test-weights.txt` only balances
the shares; a stale weight makes a share slower, never a test unrun. A share
fails if a package, or a test of a cut package, did not report exactly once.
The job summary of each share shows its slowest packages and tests; those
numbers are what the weights file is regenerated from. Run a share locally
with `go run .github/scripts/shardtests.go -shard 2/4 -profile linux -gotestflags "-count=1"`
(`-plan` prints every share and runs nothing).
The tests of a package are listed with a plain `go test -list`, which ignores
`-race` and build constraints other than this system's, so a test that exists
only under another tag is in no share. Fuzz tests and examples are listed
too, since a plain `go test` runs their seeds.

The checks the branch protection names, `test (ubuntu-latest)` and the other
two, are a job that collects the results of the platform's `static` job (format,
vet, the build of the next shell), its `shard` jobs and its `race` jobs, and
fails if any of them did not run or did not pass, or if the shares disagree
about the split (`.github/scripts/test-gate.sh`). On Windows each share runs two
packages at a time, since the runner has four cores.

A pull request that changes only documentation runs less. That means every
changed file is a `.md` or `.json` file under `docs/`, or a `.md` file at the
repository root. A rename counts as a delete and an add, so both paths must
qualify. Then:

- `test (ubuntu-latest)`, `test (windows-latest)` and `test (macos-latest)`
  still report, because the branch protection requires them. Their steps are
  skipped, except on Ubuntu, which runs the tests that read documentation
  (help, record, the ACP manifest, docgen, sitegen, deploydrift, the notices
  test in `cmd/release`, the schema, graph page and limits-quoting tests in the
  server, and the recording schema test in the workspace). A failure there is
  reported under `test (ubuntu-latest)`.
- The purge script's job is skipped.

To get the full run for such a pull request, change one file in it that is not
documentation (a comment in `.github/workflows/test.yml` will do). A push to
main and a release always run everything, so a test that reads a document from
a package the Ubuntu step does not cover is first run there. If the list of
changed files cannot be read, everything runs.

## When a test fails once

A package that fails is run again before the run counts as red
(`.github/scripts/go-test-once-more.sh` on Linux and macOS, and the matching
step on Windows). A test that fails twice fails the run. One that passes the
second time does not, but it is named in a warning annotation and in the job
summary, so a flaky test is fixed and not forgotten.

What the second run covers depends on the run:

- On a pull request, only the tests that failed. A flaky test in
  `internal/server` used to cost a second run of the whole package, over eight
  minutes on Windows.
- On a push to main, a release run or a manual run, the whole failed package.
  A test that fails only after another one has run (state the earlier one left
  behind) passes alone, so a narrow retry would hide it. Something has to give
  a failing package a full second pass before it counts as green, and this is
  where it happens.
- On any run, the whole failed package when it crashed with a panic or a fatal
  error (the test binary ended, so the tests after the failed one never ran),
  when it failed with no test to name (a build failure or a timeout), or when
  the caller passed its own `-run`.

`.github/scripts/go-test-once-more-test.sh` runs the script against small
scratch packages for each of these cases, and the Linux job runs it.

The shares of the test jobs (`.github/scripts/shardtests.go -retry`) do the same
for the packages and tests of their share. A pull request runs again only what
failed. Any other run (`-retry-all`) runs again the whole failed package, or for
a package that is cut across shares the tests of that share, and a package that
crashed leaves its later tests unreported, so they are run again too. A share
fails if what it ran did not report exactly once.

## Why Windows is slower, and what was measured

Windows is two to ten times slower than Linux on the packages that start many
processes and make many files: in one run `internal/gitx` took 600 s on Windows
against 59 s on Linux, `internal/workspace` 501 s against 110 s, and
`internal/server` 508 to 731 s against 180 to 350 s. Four runners were measured
(`ci/exp-windows-speed`, never merged).

- Defender is not the cause. The hosted Windows runner has real-time
  protection off and `C:\` and `D:\` excluded already, so exclusions add
  nothing: `internal/gitx` alone took 233, 313 and 301 s without them and 278,
  213 and 284 s with them.
- Where the files live is the likeliest cause, and moving them cut the median by
  about a third. The checkout and the runner's temp directory are on
  `D:`, but `TEMP` (where every test makes its files and git repositories) and
  the Go build cache are on `C:`. Creating 3000 small files took 5 to 8 s on
  `C:` and about 1 s on `D:`. The Windows job therefore points `TEMP`, `TMP`,
  `GOCACHE` and `GOTMPDIR` at a directory under the runner's temp directory.
  Whole `go test ./...` on Windows, three runs each, in seconds: `C:` 799, 829,
  807; `TEMP` only on `D:` 666, 779 and 545 (one package failed on a flaky test
  in that run); `TEMP` and the build cache on `D:` 541, 796, 529.
- A run's machine matters as much as the setting. The same variant ranged up to
  twice between runs, and run alone, `internal/gitx` took 180 to 310 s where the
  full run gave it 560 to 600 s: the packages share four cores.
