#!/bin/sh
# Deploys a Deneb relay into a server's Docker (implementation.md §3 M4):
# host network on two high ports (DERP over TCP, STUN over UDP), at most
# 1 CPU and 256 MB, data in /opt/deneb-relay/data. It changes nothing on
# the host's network, firewall or other services, hk-spare's own derper
# included: with --network host Docker adds no rules, and the relay only
# listens on its two ports, which must be free. Run it only when the main
# session says the acceptance relays may go up.
#
#   HOST=hk-spare IPV4=198.51.100.4 JOIN=deneb-join-… deploy/relay/deploy.sh   # the first time
#   HOST=hk-spare IPV4=198.51.100.4 deploy/relay/deploy.sh                     # later
#
# JOIN is the join code from relay.join (the client's "用我自己的服务器") or
# deneb-control relay-add (a public relay); TOKEN, a bare registration
# token, works too. Either works once, within the hour, and the relay keeps
# its secret in the data directory after. REGION names it (香港 by
# default), so it asks no cloud's metadata. CONTROL and CONTROL_PIN
# default to the control server on hk-155.
#
# Redeploying keeps the data directory, so the certificate and with it the
# pin devices trust stay the same. The old image stays as deneb-relay:prev;
# to go back: docker rm -f deneb-relay, then the same docker run with it.
set -eu
HOST=${HOST:?HOST=hk-155 or hk-spare}
IPV4=${IPV4:?IPV4: the address devices reach the relay at}
DERP_PORT=${DERP_PORT:-47445}
STUN_PORT=${STUN_PORT:-47478}
CONTROL=${CONTROL:-https://103.42.31.155:47443}
CONTROL_PIN=${CONTROL_PIN:-sha256:a90009bbefa6cbcad0971350099e2bb54c4f9968a8cdf1401ab54c6861cf0dd4}
TOKEN=${TOKEN:-}
JOIN=${JOIN:-}
REGION=${REGION:-香港}
NAME=deneb-relay
TAG=deneb-relay:v0
REMOTE=/opt/deneb-relay

cd "$(dirname "$0")/../.."
ARCHES=amd64 deploy/relay/build.sh

# Both ports must be free, unless our own container is the one holding
# them; and a relay never registered needs its token.
ssh "$HOST" "
	if [ -z \"\$(docker ps -q --filter name=^$NAME\$)\" ]; then
		if [ -n \"\$(ss -ltnH 'sport = :$DERP_PORT')\" ]; then
			echo 'TCP port $DERP_PORT is taken on $HOST:'; ss -ltnpH 'sport = :$DERP_PORT'; exit 1
		fi
		if [ -n \"\$(ss -lunH 'sport = :$STUN_PORT')\" ]; then
			echo 'UDP port $STUN_PORT is taken on $HOST:'; ss -lunpH 'sport = :$STUN_PORT'; exit 1
		fi
	fi
	if [ ! -f $REMOTE/data/relay.json ] && [ -z '$TOKEN$JOIN' ]; then
		echo 'the relay on $HOST has not registered yet: give JOIN (or TOKEN)'; exit 1
	fi
	mkdir -p $REMOTE/image/bin/amd64 $REMOTE/image/skel $REMOTE/data"
scp -q deploy/relay/bin/amd64/deneb-relay "$HOST:$REMOTE/image/bin/amd64/deneb-relay"
scp -q deploy/relay/Dockerfile "$HOST:$REMOTE/image/Dockerfile"
scp -q deploy/relay/skel/.keep "$HOST:$REMOTE/image/skel/.keep"

ssh "$HOST" "
	set -e
	chown -R 65534:65534 $REMOTE/data
	chmod 700 $REMOTE/data
	if docker inspect $NAME >/dev/null 2>&1; then
		docker tag \$(docker inspect -f '{{.Image}}' $NAME) deneb-relay:prev
	fi
	docker build -q -t $TAG $REMOTE/image
	stamp=\$(date -u +%Y%m%d-%H%M%S)
	mkdir -p $REMOTE/logs $REMOTE/backups
	if docker inspect $NAME >/dev/null 2>&1; then
		docker logs $NAME > $REMOTE/logs/$NAME-\$stamp.log 2>&1
		ls -1t $REMOTE/logs/$NAME-*.log | tail -n +31 | xargs -r rm -f
		docker stop -t 10 $NAME >/dev/null
		cp -a $REMOTE/data $REMOTE/backups/data-\$stamp
		ls -1dt $REMOTE/backups/data-* | tail -n +6 | xargs -r rm -rf
	fi
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--cpus 1 --memory 256m --memory-swap 256m --pids-limit 256 \
		--log-opt max-size=10m --log-opt max-file=3 \
		-v $REMOTE/data:/data \
		${TOKEN:+-e DENEB_RELAY_TOKEN=$TOKEN} ${JOIN:+-e DENEB_RELAY_JOIN=$JOIN} \
		$TAG -control $CONTROL -control-pin $CONTROL_PIN -data /data \
		-listen :$DERP_PORT -stun :$STUN_PORT -ipv4 $IPV4 -region $REGION
	sleep 3
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Status}}'
	docker logs --tail 20 $NAME"
