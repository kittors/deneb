#!/bin/sh
# Deploys deneb-front (cmd/deneb-front), an entrance to the control server,
# onto one machine, in Docker: host network, at most 0.5 CPU and 128 MB
# (design/plan/control-reachability-20261116.md). It listens on one port
# and changes nothing on the host's network, firewall or other containers:
# with --network host and no published port Docker adds no rules.
#
#   HOST=hk-spare deploy/front/deploy.sh
#
# Its file is deploy/front/front.$HOST.env (CONFIG to choose another): where
# it listens and the control server's -front-listen. It is not in the
# repository; front.example.env shows the shape. The control server must
# list this machine in -front-from first, or it closes the connections.
#
# Before starting it, it checks the port is free and the control server
# answers from that machine. docker ps and the listening ports are printed
# before and after, to compare.
#
# To take it away: ssh $HOST docker rm -f deneb-front
set -eu
HOST=${HOST:?HOST=<machine> deploy/front/deploy.sh}
CONFIG=${CONFIG:-deploy/front/front.$HOST.env}
NAME=deneb-front
TAG=deneb-front:v0
REMOTE=/opt/deneb-front

cd "$(dirname "$0")/../.."
test -f "$CONFIG" || { echo "no file at $CONFIG (see deploy/front/front.example.env)" >&2; exit 1; }
LISTEN=$(sed -n 's/^LISTEN=//p' "$CONFIG" | tail -n 1)
TO=$(sed -n 's/^DENEB_FRONT_TO=//p' "$CONFIG" | tail -n 1)
case "$LISTEN" in :[0-9]*) ;; *) echo "LISTEN: a port, as :47443" >&2; exit 2 ;; esac
case "$TO" in *[!A-Za-z0-9.:\[\]-]* | "") echo "DENEB_FRONT_TO: host:port" >&2; exit 2 ;; esac
port=${LISTEN#:}
ARCHES=amd64 deploy/front/build.sh

snapshot() {
	ssh "$HOST" "echo '== docker'; docker ps --format '{{.Names}} {{.Image}} {{.Status}}' | sort; echo '== listening'; ss -ltnuH | awk '{print \$1, \$5}' | sort -u"
}
echo "### before"
snapshot

ssh "$HOST" "mkdir -p $REMOTE/image/bin/amd64"
scp -q deploy/front/bin/amd64/deneb-front "$HOST:$REMOTE/image/bin/amd64/deneb-front"
scp -q deploy/front/Dockerfile "$HOST:$REMOTE/image/Dockerfile"
ssh "$HOST" "
	set -e
	if docker ps --format '{{.Names}}' | grep -qx $NAME; then :
	elif ss -ltnH 'sport = :$port' | grep -q .; then
		echo 'port $port is taken on $HOST: not started' >&2
		exit 1
	fi
	host=\$(echo '$TO' | sed 's/:[0-9]*\$//; s/^\[//; s/\]\$//')
	if ! timeout 5 bash -c \"exec 3<>/dev/tcp/\$host/${TO##*:}\" 2>/dev/null; then
		echo 'the control server ($TO) does not answer from $HOST: not started' >&2
		exit 1
	fi
	docker build -q -t $TAG $REMOTE/image >/dev/null
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 256 \
		--ulimit nofile=16384:16384 \
		--log-opt max-size=5m --log-opt max-file=2 \
		-e DENEB_FRONT_TO='$TO' \
		$TAG -listen '$LISTEN' >/dev/null
	sleep 2
	docker logs --tail 5 $NAME"

echo "### after"
snapshot
