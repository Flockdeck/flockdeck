# Running Flockdeck on a server

Flockdeck does not need a screen. `-no-window` and `-detach` skip the window
and the browser entirely, and no browser is ever looked for, so a bare Linux
box, a Raspberry Pi, a NAS or a cheap VPS can run it. The agents run on hardware
you already have or already pay for, and your code stays on a machine you
control.

```sh
flockdeck -no-window       # on the server: serve headless and print the URL
flockdeck remote enable    # with Flockdeck Remote: enrol it with the relay
flockdeck remote pair      # a one-time link and QR code for your phone or laptop
```

The last two steps are the optional Flockdeck Remote add-on. Without it, the
server keeps running and prints a URL on its own loopback address, which only
that machine can open. To the
relay and to a paired device a server is a desktop like any other, so tabs,
panes, chat on a phone and fan out all work on it. The [Flockdeck Remote
page](../internal/help/pages/remote.md) has the details, including the short
list of actions a paired device is refused for its own safety (quitting,
updating, setting API keys and a few more). Do those from a terminal on the
server, over SSH.

`-detach` also gives the terminal back, so an SSH session or a `nohup` can end
without ending Flockdeck. Under a process supervisor such as systemd, plain
`-no-window` is usually the better fit. It keeps the terminal as its interface
and leaves the process's lifecycle and logs to the supervisor.

## Docker

The headless run also comes as a container image, built from the `Dockerfile` at
the repository root. It is based on Alpine rather than a from-scratch image so
that a pane's shell and agent CLI have somewhere to run. The Dockerfile's
comments say why, and list every `FLOCKDECK_*` variable it reads (`flockdeck -h`
lists them too). The image sets `FLOCKDECK_UPDATE=off`, because a container is
replaced by pulling a new tag and not by updating itself in place.

The server inside binds `127.0.0.1` only, on a port chosen at random at each
start, exactly as it does outside a container. So `docker run -p` publishes
nothing. You reach it with `docker exec`, with `--network host`, or with
`flockdeck remote enable`, which uses an outward connection and needs no inbound
port. The `EXPOSE` comment in the Dockerfile has the detail. With `--network host`,
browse to `127.0.0.1` or `localhost` on the port it prints; the host's own name or
LAN address gets a 403 until it is listed in `FLOCKDECK_ALLOWED_HOSTS`.

```sh
docker build -t flockdeck .
docker run -d --name flockdeck -v flockdeck-state:/home/flockdeck -v "$PWD:/workspace" flockdeck
docker exec -it flockdeck flockdeck    # prints the URL of the instance already running
```

## Kubernetes

`deploy/helm/flockdeck/` is a Helm chart built on the same image. It runs one pod
(there is no replica count, because a saved layout and an instance record belong
to one process), a `PersistentVolumeClaim` for that state, and pod hardening: a
non-root user, a read-only root filesystem and no Linux capabilities.

Because the server binds loopback on a random port, the pod also runs a small
`flockdeck-portproxy` sidecar. It listens on a fixed port and forwards to the
server, and the chart's `Service` routes to it. The chart ships no `Ingress`.
The [chart README](../deploy/helm/flockdeck/README.md) has the install guide,
what the `Service` does and does not authenticate, and how to reach the server:
`kubectl exec` plus `kubectl port-forward`, or `flockdeck remote enable` from
outside the cluster.

The server also refuses a request whose `Host` header is not `127.0.0.1` or
`localhost` (on any port) or a name listed in `FLOCKDECK_ALLOWED_HOSTS`. That is
what stops a web page from reaching it under a name of its own. The chart lists
the `Service`'s names for you, and its `allowedHosts` value takes an `Ingress`
hostname or a `NodePort` or `LoadBalancer` address. Outside the chart, set the
variable where the server runs: a comma-separated list, a bare name for any
port or `name:port` for one. A `kubectl port-forward` or `ssh -L` to any local
port needs nothing, as long as the browser uses `127.0.0.1` or `localhost`. The
help page "A page says Flockdeck did not answer, naming the Host header" has the
rest.
