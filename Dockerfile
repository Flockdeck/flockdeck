# syntax=docker/dockerfile:1

# Flockdeck, self-hosted: a headless instance (`flockdeck -no-window`) running
# in a container, for a server you own rather than the desktop app.
#
# Unlike flockdeck-relay's Dockerfile (distroless, nonroot -- see
# deploy/helm/flockdeck-relay/ in the relay repo, and
# flockdeck-planning/12-installation-simplification.md's base-image spike),
# this image is built on Alpine rather than distroless/static. The relay is a
# stateless HTTP server that never spawns a child process; Flockdeck's entire
# job is spawning and supervising child processes inside panes -- a shell,
# git, whatever agent CLI the operator runs (claude, codex, ...) -- and a
# distroless/static image ships none of those: no shell, no coreutils, no
# package manager. Alpine keeps "open a pane, get a working shell" true out of
# the box, and gives a self-hoster `docker exec` + `apk add` to install
# whatever agent CLI they use, without waiting on a rebuild of this image.
# (A second, leaner `-slim` tag on distroless, for operators who only ever
# derive their own image FROM this one and never need a shell here, is future
# work the installation-simplification plan flags but does not require now.)

# ---- build ------------------------------------------------------------
FROM golang:1.27 AS build
ARG VERSION=dev
WORKDIR /src

# Dependencies first, so an edit to the source alone reuses this layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0: a static binary, so the final stage needs no libc beyond
# Alpine's own (musl) and cross-building from another host works unchanged.
# -trimpath and -ldflags "-s -w": no build-machine paths or debug symbols in
# a binary that ships to other people's servers. -X main.version: the same
# flag cmd/release uses for desktop builds (release.yml), so `flockdeck
# -version` and the update checker agree with the tag this image was built
# from.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
	-o /out/flockdeck .

# ---- runtime ------------------------------------------------------------
FROM alpine:3.20

# ca-certificates: every agent that talks to a model API over HTTPS needs
# these, and so does the (disabled by default here, see FLOCKDECK_UPDATE
# below) update checker.
# git, bash: the two things nearly every pane either is, or shells out to;
# shipping them means a pane's first command works before anyone has run
# `apk add` by hand.
# tini: Flockdeck is not a stateless request handler, it supervises child
# processes for as long as the container runs. Running it as PID 1 directly
# means it alone is responsible for reaping every grandchild a pane's shell
# or agent CLI forks and leaves behind (a `git` that backgrounds a credential
# helper, for instance) -- something nothing in this image otherwise does.
# tini does that reaping and forwards signals (SIGTERM, SIGHUP) straight
# through to Flockdeck's own handling of them (main.go's signal.Notify),
# instead of Flockdeck receiving them as PID 1 with no init in front of it.
# jq: parses the instance record HEALTHCHECK reads (see healthcheck.sh) --
# small, and far more trustworthy than hand-rolled shell string surgery on
# JSON for something a health check's correctness depends on.
RUN apk add --no-cache ca-certificates git bash tini jq \
	&& addgroup -g 65532 flockdeck \
	&& adduser -D -u 65532 -G flockdeck -h /home/flockdeck -s /bin/bash flockdeck \
	&& mkdir -p /workspace \
	&& chown -R flockdeck:flockdeck /workspace /home/flockdeck

COPY --from=build /out/flockdeck /usr/local/bin/flockdeck
COPY --chmod=755 docker/healthcheck.sh /usr/local/bin/flockdeck-healthcheck
COPY LICENSE THIRD-PARTY-NOTICES.md /licenses/

USER flockdeck:flockdeck
ENV HOME=/home/flockdeck
# Every agent's own settings, the saved layout, the recent-projects list and
# the per-run instance record (internal/store.Dir(), which os.UserConfigDir
# resolves under $HOME on Linux) live under here, so it is the one path that
# has to survive a restart for a self-hoster to keep their layout and agent
# settings across pulls of a newer image. It holds no long-lived secret:
# self-hosted service mode's auth token is generated fresh each run and is
# never the same twice (see internal/server.New), so there is nothing in
# this directory a restart cannot regenerate -- only convenience is lost if
# it is not persisted.
#
# /workspace is the project Flockdeck opens (-C, or $FLOCKDECK_DIR): mount
# your repository there, or leave it empty to start with nothing open.
VOLUME ["/home/flockdeck", "/workspace"]
WORKDIR /workspace

# Background update checks download and stage a new binary over this one
# (cli_update.go), which makes no sense for an image that is meant to be
# redeployed by pulling a new tag, not by mutating itself in place -- and
# would try to write to a location a read-only root filesystem (as the Helm
# chart's pod hardening sets) refuses anyway. FLOCKDECK_UPDATE=off is exactly
# the escape hatch main.go documents for this ("anyone packaging Flockdeck
# for somewhere with its own updater"); override it if you deliberately want
# a long-running container to self-update instead of being redeployed.
ENV FLOCKDECK_UPDATE=off

# Documentation only, not a working port to publish with `docker run -p`:
# self-hosted service mode's server always listens on 127.0.0.1 only, on a
# random port chosen at each start, with a fresh per-run token as its only
# auth (internal/server.New) -- it deliberately ships no bind-address flag or
# persistent token (see the correction in
# flockdeck-planning/12-installation-simplification.md). That is loopback
# *inside this container's network namespace*, so publishing a host port
# with `-p` reaches nothing. Three ways to actually reach the web UI:
#   1. `docker exec -it <container> flockdeck` prints the URL of the
#      instance already running, to open by hand from inside the container
#      (or over `docker exec -it <container> sh` and curl it locally).
#   2. `docker run --network host ...` puts this container's loopback on the
#      host's, so 127.0.0.1:<the printed port> is directly reachable from
#      the host machine. (Linux only; not the container's default because it
#      also gives the container the host's whole loopback, not just this
#      port.)
#   3. `flockdeck remote enable` (see FLOCKDECK_RELAY, FLOCKDECK_REMOTE_JOIN,
#      FLOCKDECK_REMOTE_INVITE, FLOCKDECK_REMOTE_NAME below) enrols this
#      instance with a relay it dials out to, which is the real answer for
#      reaching it from a phone or a browser off this machine -- no inbound
#      port needed at all. README.md's "Self-hosted" section covers this.
# On Kubernetes, `kubectl port-forward` reaches loopback-bound processes
# inside a pod's network namespace the same way `docker exec` does inside a
# container's -- see deploy/helm/flockdeck/README.md.
EXPOSE 8080

# Runs as the flockdeck user, so it can read its own instance record
# ($HOME/.config/flockdeck/instance.json) and reach its own loopback server
# with the token from it -- the same probe `flockdeck` itself makes before
# attaching to a running instance (internal/server.Probe), asked here for
# `docker inspect`/`kubectl get pods` instead of a person watching a
# terminal.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD ["/usr/local/bin/flockdeck-healthcheck"]

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/flockdeck"]
# -no-window: print the URL and keep serving, the default and point of this
# image (see EXPOSE above for how to reach it). Every other setting comes
# from the environment (FLOCKDECK_DIR, FLOCKDECK_START_AGENT, FLOCKDECK_FRESH,
# FLOCKDECK_SHELL_FIRST, FLOCKDECK_SOLO, FLOCKDECK_RELAY,
# FLOCKDECK_REMOTE_JOIN/_INVITE/_NAME -- `flockdeck -h` lists them all) or a
# flag appended after the image name on `docker run`, which -- like any
# argv given after ENTRYPOINT -- replaces this default rather than adding to
# it; flags given that way still beat any of the environment variables above,
# same as running flockdeck by hand.
CMD ["-no-window"]
