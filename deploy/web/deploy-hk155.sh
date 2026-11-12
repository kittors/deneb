#!/bin/sh
# Deploys the Web version (deneb-web and the browser app) into hk-155's
# Docker, next to the control server (design/plan/m7/web-deploy.md): host
# network, listening on 127.0.0.1 only, at most half a CPU and 128 MB,
# read-only. nginx, in front, serves the name over HTTPS (nginx-hk155.sh);
# deneb-web passes /api/ on to the control server on this machine, trusting
# its pinned key as devices do. It changes nothing on the host's network,
# firewall or nginx, nor any other container.
#
#   deploy/web/deploy-hk155.sh          # build, upload, (re)start, check
#
# The name and the port come from deploy/web/site.hk155.env (not in the
# repository; see site.example.env). The image running before stays as
# deneb-web:prev, for rollback-hk155.sh. deneb-web keeps nothing: a
# redeploy only cuts the browsers' streams, which reconnect.
set -eu
HOST=${HOST:-hk-155}
SITE=${SITE:-deploy/web/site.hk155.env}
CONTROL_PORT=${CONTROL_PORT:-47443}
NAME=deneb-web
TAG=deneb-web:v0
REMOTE=/opt/deneb-web
CONTROL_DATA=/opt/deneb-control/data

cd "$(dirname "$0")/../.."
test -f "$SITE" || { echo "no site settings at $SITE (copy deploy/web/site.example.env)" >&2; exit 1; }
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_PORT:?in $SITE}"
ARCHES=amd64 deploy/web/build.sh

# The port must be free, unless our own container is the one holding it;
# the control server's pin must be there to trust it by.
ssh "$HOST" "
	set -e
	if [ -z \"\$(docker ps -q --filter name=^$NAME\$)\" ] && [ -n \"\$(ss -ltnH 'sport = :$WEB_PORT')\" ]; then
		echo 'port $WEB_PORT is taken on $HOST:'; ss -ltnpH 'sport = :$WEB_PORT'; exit 1
	fi
	test -s $CONTROL_DATA/tls/pin.txt || { echo 'no control server pin at $CONTROL_DATA/tls/pin.txt'; exit 1; }
	rm -rf $REMOTE/image
	mkdir -p $REMOTE/image/bin/amd64"
scp -q deploy/web/bin/amd64/deneb-web "$HOST:$REMOTE/image/bin/amd64/deneb-web"
scp -q deploy/web/Dockerfile "$HOST:$REMOTE/image/Dockerfile"
scp -qr deploy/web/app "$HOST:$REMOTE/image/app"

ssh "$HOST" "
	set -e
	# The image running now stays as deneb-web:prev, for rollback-hk155.sh.
	if docker inspect $NAME >/dev/null 2>&1; then
		docker tag \$(docker inspect -f '{{.Image}}' $NAME) deneb-web:prev
	fi
	docker build -q -t $TAG $REMOTE/image
	pin=\$(cat $CONTROL_DATA/tls/pin.txt)
	docker rm -f $NAME >/dev/null 2>&1 || true
	docker run -d --name $NAME \
		--network host \
		--restart unless-stopped \
		--read-only \
		--cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 128 \
		--log-opt max-size=10m --log-opt max-file=3 \
		$TAG -static /app -listen 127.0.0.1:$WEB_PORT \
		-control https://127.0.0.1:$CONTROL_PORT -control-pin \$pin
	sleep 2
	docker ps --filter name=^$NAME\$ --format '{{.Names}} {{.Status}}'
	docker logs --tail 5 $NAME
	command -v curl >/dev/null || { echo 'no curl here to check it with'; exit 0; }
	app=\$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$WEB_PORT/)
	api=\$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$WEB_PORT/api/v1/userview)
	echo \"the app: \$app (want 200); the API through it: \$api (want 401: reached the control server, not logged in)\"
	[ \"\$app\" = 200 ] && [ \"\$api\" = 401 ]"
