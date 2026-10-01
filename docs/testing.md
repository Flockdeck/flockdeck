# Writing tests that stay isolated

A test must never read the config directory of the person running it, use a
key they have stored, or reach a real host. Flockdeck keeps `agents.json`,
`keys.json` (including the stored TypeSafe/Jev key) and the rest of its state
under `os.UserConfigDir()`, so a test that resolves it on a developer's machine
reads their real files, and a stored key sends the test's fixture task text to
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
  `store.Dir`) give a throwaway place on Windows, macOS and Linux;
- unsets every `*_API_KEY`, `*_API_TOKEN` and `*_AUTH_TOKEN` in the environment;
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
  under test its URL (`routejev.APIBase = srv.URL` and the like). Never a real
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

If a guard trips, the message names what was reached; fix the test rather than
the guard. `internal/testiso/testiso_test.go` holds the regression tests that
prove each guard trips.
