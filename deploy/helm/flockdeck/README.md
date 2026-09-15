# flockdeck (Helm chart)

Flockdeck, self-hosted, on a Kubernetes cluster you already run: a single
pod running `flockdeck -no-window`, built on the same image the plain
Docker install uses (the repository's `Dockerfile`; see its own comments for
the base-image and hardening choices).

There is no replica count. A saved layout, a recent-projects list and a
per-run instance record all belong to one process; a second pod would be a
second Flockdeck with its own agents, not a second view onto the first's.

## Install

```sh
helm install my-flockdeck oci://ghcr.io/jmwri/charts/flockdeck --version 0.1.0
```

(OCI publishing to `ghcr.io/jmwri/charts` is CI work this chart's own
`../../.github/workflows/` does not yet build -- see the repository's
installation-simplification plan. Until then, install straight from a
checkout: `helm install my-flockdeck ./deploy/helm/flockdeck`.)

At minimum, decide:

- **`persistence.state.enabled`** (default `true`): keep the saved layout,
  recent-projects list and agent settings across restarts. Turn it off only
  for a throwaway try.
- **`persistence.workspace.enabled`** (default `false`): mount a project at
  `/workspace` (or `dir`, if set). The chart does not populate it --
  `extraInitContainers` is where a `git clone` step goes, or point
  `persistence.workspace.existingClaim` at a volume you already filled.
- **`resources`**: flockdeck runs every agent's own process (a shell, git,
  an agent CLI) inside its one pod, unlike a typical stateless web app, so
  give it real headroom -- the defaults are a starting point, not a ceiling
  most workloads should expect to hit.

## Reaching the web UI

Read this before you install, because it is the one thing this chart cannot
paper over: self-hosted service mode's server binds `127.0.0.1` only, inside
the pod, on a port chosen at random each start, with a fresh per-run token as
its only auth. There is no bind-address flag and no persistent token (see
`internal/server.New` in the main repository, and the correction recorded in
`flockdeck-planning/12-installation-simplification.md`) -- so there is no
fixed, predictable address a Kubernetes `Service` or an `Ingress` could ever
route to, and this chart ships neither rather than ship one that looks like
it works and does not.

Two ways to actually reach it, in order of how permanent they are:

1. **For now, from inside the cluster's reach:** `kubectl exec` the pod to
   read its instance record, then `kubectl port-forward` that exact port.
   `kubectl port-forward` reaches a loopback-bound process just fine --
   it runs its forwarding helper inside the pod's own network namespace,
   the same one `127.0.0.1` refers to -- but only for the specific port
   named at the time, which changes on every restart. `helm install`'s own
   NOTES (`helm get notes <release>` later) has the exact commands.

2. **For real, from anywhere -- a phone, a browser off this cluster, no
   inbound access to the cluster at all:** enrol the instance with a relay,
   the same remote-access mechanism the desktop app uses, over an outbound
   connection this pod makes itself:

   ```sh
   kubectl exec -it deploy/<release>-flockdeck -- flockdeck remote enable
   kubectl exec -it deploy/<release>-flockdeck -- flockdeck remote pair
   ```

   `values.yaml`'s `remote.*` fields seed `flockdeck remote enable`'s
   defaults (its relay URL, and a join/invite code from
   `remote.existingSecret` or the chart's own Secret) so the bare command
   above needs nothing typed at the terminal; enrolling is still a
   deliberate, one-time step you run yourself, not something a pod does on
   its own at start-up, the same way `flockdeck-relay`'s own chart has you
   run `... invite` by `kubectl exec` rather than at install.

A sidecar that proxies a fixed port to the pod's own random loopback port
(containers in one pod share a network namespace, so this is possible without
any change to flockdeck itself) would let a normal `Service`/`Ingress` work
after all; it is deliberately not built here; it is real, scoped follow-up
work, not something to guess at and ship unverified with no cluster to test
it against.

## Values

See `values.yaml`, commented inline. The notable groups:

- `image.*` -- repository/tag/pullPolicy.
- `dir`, `agent`, `fresh`, `shellFirst`, `solo` -- `flockdeck`'s own
  top-level flags, taken as env vars (`FLOCKDECK_DIR`,
  `FLOCKDECK_START_AGENT`, `FLOCKDECK_FRESH`, `FLOCKDECK_SHELL_FIRST`,
  `FLOCKDECK_SOLO`); `flockdeck -h` in the image documents each.
- `remote.*` -- relay enrolment (see above); `apiKey.*` -- the fallback key
  an API agent uses.
- `persistence.state.*` / `persistence.workspace.*` -- the two volumes
  (see above).
- `extraEnv`, `extraArgs`, `extraInitContainers`, `extraVolumes` /
  `extraVolumeMounts` -- escape hatches for anything not yet promoted to a
  first-class value here.

## Testing this chart

`../../chart_test.go`, in the main repository, reads `flockdeck`'s actual
flag definitions and every `FLOCKDECK_*` name its own source mentions, and
fails the build if this chart sets an environment variable or passes a flag
the binary does not read, or reads one this chart has no way to set -- the
same drift check `flockdeck-relay`'s own chart has (`cmd/flockdeck-relay/chart_test.go`
there). Run it with `go test ./... -run Chart` from the repository root.

`helm lint --strict` and `helm template` (a real cluster is not needed for
either) are worth running against any local change; wiring them, and a
`kind`-based install smoke test, into CI is scoped but not yet built -- see
the installation-simplification plan's own note that the relay chart never
got that test either, and the intent here not to repeat the gap
indefinitely.
