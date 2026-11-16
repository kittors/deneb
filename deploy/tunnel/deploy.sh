#!/bin/sh
# Deploys cloudflared for the Cloudflare tunnel onto hk-155, in Docker
# (design/plan/control-reachability-20261116.md §9.4, §10). Host network,
# so that it reaches the control server's -tunnel-listen (127.0.0.1:47450)
# and the public relay's (127.0.0.1:47451). It only connects out, to
# Cloudflare: devices reach nothing of it (its status page is loopback only),
# and with --network host and no published port Docker adds no rules. At
# most 0.5 CPU and 128 MB; the image's version is pinned.
#
#   deploy/tunnel/deploy.sh            # HOST=hk-155
#
# The tunnel and its two public host names are made in Cloudflare's
# dashboard (Zero Trust → Networks → Tunnels), by the user: the control
# server's → http://127.0.0.1:47450, the relay's → http://127.0.0.1:47451.
# The host names stay there and in the deployment's own notes, never in
# this repository (deneb.app in its examples).
#
# The tunnel's token: the user puts it in /opt/deneb-tunnel/token on hk-155,
# the token alone on one line. This script never reads it, prints it, or
# puts it on a command line: it makes the file 0600 and the image's user's
# (65532, distroless's nonroot), mounts it read-only, and cloudflared reads
# it itself (--token-file, in 2026.11.0).
#
# docker ps and the listening ports are printed before and after, to
# compare. To take it away: ssh hk-155 docker rm -f deneb-tunnel
set -eu
HOST=${HOST:-hk-155}
NAME=deneb-tunnel
IMAGE=cloudflare/cloudflared:2026.11.0
TOKEN=/opt/deneb-tunnel/token
RUNAS=65532:65532
# cloudflared's own status page (metrics, its configuration with the host
# names, debug handlers): in a container it listens on every interface
# unless told otherwise (metrics.Runtime "virtual" in 2026.11.0), and with
# the host network that is the Internet. Loopback only, on a port of its own.
METRICS_PORT=47452

snapshot() {
	ssh "$HOST" "echo '== docker'; docker ps --format '{{.Names}} {{.Image}} {{.Status}}' | sort; echo '== listening'; ss -ltnuH | awk '{print \$1, \$5}' | sort -u"
}
echo "### before"
snapshot

ssh "$HOST" "
	set -e
	if [ ! -s $TOKEN ]; then
		echo 'no token at $HOST:$TOKEN: the user puts it there, the token alone on one line (see deploy/tunnel/deploy.sh)' >&2
		exit 1
	fi
	if [ \"\$(wc -l <$TOKEN)\" -gt 1 ]; then
		echo '$TOKEN: the token alone, on one line' >&2
		exit 1
	fi
	chown $RUNAS $TOKEN
	chmod 600 $TOKEN
	if [ -z \"\$(docker ps -q --filter name=^$NAME\$)\" ] && ss -ltnH 'sport = :$METRICS_PORT' | grep -q .; then
		echo 'port $METRICS_PORT on $HOST is taken: cloudflared keeps its status page there, on loopback (METRICS_PORT in deploy/tunnel/deploy.sh)' >&2
		exit 1
	fi
	docker pull -q $IMAGE >/dev/null
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--user $RUNAS \
		--cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 128 \
		--log-opt max-size=5m --log-opt max-file=2 \
		-v $TOKEN:/etc/cloudflared/token:ro \
		$IMAGE tunnel --no-autoupdate --metrics 127.0.0.1:$METRICS_PORT run --token-file /etc/cloudflared/token >/dev/null
	sleep 5
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Image}} {{.Status}}'
	docker logs --tail 10 $NAME 2>&1"

echo "### after"
snapshot
