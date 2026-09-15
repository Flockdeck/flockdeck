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

Read this before you install: self-hosted service mode's server binds
`127.0.0.1` only, inside the pod, on a port chosen at random each start, with
a fresh per-run token as its only auth. There is no bind-address flag and no
persistent token (see `internal/server.New` in the main repository, and the
correction recorded in `flockdeck-planning/12-installation-simplification.md`)
-- so flockdeck's own container still has no fixed, predictable address of
its own for a `Service` to route to directly.

What this chart does about that is a small sidecar container,
`flockdeck-portproxy` (`cmd/portproxy` in the main repository, built into the
same image), that sits alongside flockdeck in the same pod. Containers in one
pod share a network namespace, so it can do what flockdeck's own server
cannot: listen on a fixed port (`sidecar.port`, default `8080`) and forward
every connection, byte for byte, to whatever port flockdeck actually bound
this run -- discovered from the instance record flockdeck writes at
start-up, the same one `docker/healthcheck.sh` already reads. It carries no
authorization logic of its own: it forwards flockdeck's own auth challenge
completely unchanged, so reaching it without the token gets the same 403
flockdeck would give directly. This is what makes a real `Service`
possible (`service.enabled`, default `true`), and it is what that Service
routes to -- never to flockdeck's own container, which still declares no
`containerPort` at all.

Three ways to actually reach it, in order of how permanent they are:

1. **The Service, from inside the cluster (or fronted by your own Ingress
   or gateway):** `http://<release>-flockdeck.<namespace>.svc:<service.port>/?t=<token>`,
   where `<token>` still comes from the instance record (see below) --
   the Service and its sidecar fix the address, not the auth. This is the
   one actually new here: point your own Ingress, gateway, or another
   in-cluster workload at this Service the way you would any other.
   `Ingress` itself is not shipped by this chart (see "Values" below).

2. **For now, from outside the cluster's reach, without relying on the
   Service:** `kubectl exec` the pod to read its instance record, then
   `kubectl port-forward` that exact port. `kubectl port-forward` reaches a
   loopback-bound process just fine -- it runs its forwarding helper inside
   the pod's own network namespace, the same one `127.0.0.1` refers to --
   but only for the specific port named at the time, which changes on every
   restart. `helm install`'s own NOTES (`helm get notes <release>` later)
   has the exact commands.

3. **For real, from anywhere -- a phone, a browser off this cluster, no
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

The token itself is not exposed by the Service or the sidecar; get it the
same way regardless of which path above you use:

```sh
kubectl exec -n <namespace> deploy/<release>-flockdeck -- \
  sh -c 'cat "$HOME/.config/flockdeck/instance.json"'
```

`Ingress` is deliberately left out of this chart for now -- a `Service` is
the meaningful unlock the sidecar buys, and getting an `Ingress` right (TLS,
a host, a controller's own annotations) is a bigger surface than this chart
takes a view on; put your own in front of the Service above.

## Values

See `values.yaml`, commented inline. The notable groups:

- `image.*` -- repository/tag/pullPolicy.
- `dir`, `agent`, `fresh`, `shellFirst`, `solo` -- `flockdeck`'s own
  top-level flags, taken as env vars (`FLOCKDECK_DIR`,
  `FLOCKDECK_START_AGENT`, `FLOCKDECK_FRESH`, `FLOCKDECK_SHELL_FIRST`,
  `FLOCKDECK_SOLO`); `flockdeck -h` in the image documents each.
- `remote.*` -- relay enrolment (see above); `apiKey.*` -- the fallback key
  an API agent uses.
- `sidecar.*` -- the `flockdeck-portproxy` container's fixed port and
  resources; `service.*` -- the `Service` in front of it (see "Reaching the
  web UI" above).
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
there). It also checks that the `Service` routes to the `flockdeck-portproxy`
sidecar's own named port and never to flockdeck's own (still portless)
container, and that no `ingress.yaml` has crept in. Run it with
`go test ./... -run Chart` from the repository root. `cmd/portproxy`'s own
tests (`go test ./cmd/portproxy/...`) cover the sidecar binary itself: that it
finds flockdeck's real address from the instance record, and relays bytes
both ways.

`helm lint --strict` and `helm template` (a real cluster is not needed for
either) are worth running against any local change; wiring them, and a
`kind`-based install smoke test, into CI is scoped but not yet built -- see
the installation-simplification plan's own note that the relay chart never
got that test either, and the intent here not to repeat the gap
indefinitely.
