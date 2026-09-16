#!/bin/sh
# The container's HEALTHCHECK. Flockdeck's server has an existing /health
# endpoint (internal/server/attach.go's handleHealth) built for exactly this
# -- it does the same "is the workspace still answering" check `flockdeck`
# itself makes before attaching to a running instance -- but self-hosted
# service mode deliberately has no unauthenticated status port (there is no
# bind-address flag, and the loopback server's token is the only auth, see
# the Dockerfile's EXPOSE comment). So this reads the token the same way the
# `flockdeck` binary itself does: from the instance record it wrote at
# start-up, under $HOME/.config/flockdeck (internal/store.Dir /
# instancePath), readable only by the user this container runs as.
#
# Anything short of a clean 200 from /health -- no instance record yet
# during start-up, a process that died without clearing its record, a
# workspace too wedged to answer within the timeout -- fails the check, which
# is exactly what HEALTHCHECK's --start-period and --retries exist to
# tolerate before anyone acts on it.
set -eu

instance="${HOME:-/home/flockdeck}/.config/flockdeck/instance.json"
if [ ! -f "$instance" ]; then
	echo "no instance record yet at $instance" >&2
	exit 1
fi

url=$(jq -r '.url // empty' "$instance")
token=$(jq -r '.token // empty' "$instance")
if [ -z "$url" ] || [ -z "$token" ]; then
	echo "instance record at $instance has no url/token" >&2
	exit 1
fi

wget -q -T 4 -O /dev/null "${url}/health?t=${token}"
