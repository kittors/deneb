#!/bin/sh
# Deploys the Web version (deneb-web and the browser app) into hk-spare's
# Docker (design/plan/m7/web-deploy.md): host network, listening on
# 127.0.0.1 only, at most half a CPU and 128 MB, read-only. nginx, in front,
# serves the name over HTTPS (nginx-hkspare.sh); deneb-web passes /api/ on
# to the control server on hk-155, over TLS trusted by its pinned key, as
# devices do. It changes nothing on the host's network, firewall or nginx,
# nor any other container there.
#
#   deploy/web/deploy-hkspare.sh        # build, upload, (re)start, check
#
# The name, the port and the rest come from deploy/web/site.hkspare.env (not
# in the repository; see site.example.env). The image running before stays
# as deneb-web:prev, for rollback-hkspare.sh. deneb-web keeps nothing: a
# redeploy only cuts the browsers' streams, which reconnect.
set -eu
HOST=${HOST:-hk-spare}
CONTROL_HOST=${CONTROL_HOST:-hk-155}
SITE=${SITE:-deploy/web/site.hkspare.env}

cd "$(dirname "$0")/../.."
test -f "$SITE" || { echo "no site settings at $SITE (copy deploy/web/site.example.env)" >&2; exit 1; }
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_PORT:?in $SITE}" "${CONTROL_URL:?in $SITE}"
if [ -z "${CONTROL_PIN:-}" ]; then
	CONTROL_PIN=$(ssh "$CONTROL_HOST" cat /opt/deneb-control/data/tls/pin.txt)
fi
printf '%s\n' "$CONTROL_PIN" | grep -Eq '^sha256:[0-9a-f]{64}$' ||
	{ echo "the control server's pin is not sha256:<64 hex>: $CONTROL_PIN" >&2; exit 1; }
ARCHES=amd64 deploy/web/build.sh

# The port must be free, unless our own container is the one holding it.
ssh "$HOST" sh -s -- "$WEB_PORT" <<'EOF'
set -eu
port=$1
if [ -z "$(docker ps -q --filter name=^deneb-web$)" ] && [ -n "$(ss -ltnH "sport = :$port")" ]; then
	echo "port $port is taken:"; ss -ltnpH "sport = :$port"; exit 1
fi
rm -rf /opt/deneb-web/image
mkdir -p /opt/deneb-web/image/bin/amd64
EOF
scp -q deploy/web/bin/amd64/deneb-web "$HOST:/opt/deneb-web/image/bin/amd64/deneb-web"
scp -q deploy/web/Dockerfile "$HOST:/opt/deneb-web/image/Dockerfile"
scp -qr deploy/web/app "$HOST:/opt/deneb-web/image/app"

ssh "$HOST" sh -s -- "$WEB_PORT" "$CONTROL_URL" "$CONTROL_PIN" <<'EOF'
set -eu
port=$1 control=$2 pin=$3
# The image running now stays as deneb-web:prev, for rollback-hkspare.sh.
if docker inspect deneb-web >/dev/null 2>&1; then
	docker tag "$(docker inspect -f '{{.Image}}' deneb-web)" deneb-web:prev
fi
docker build -q -t deneb-web:v0 /opt/deneb-web/image
docker rm -f deneb-web >/dev/null 2>&1 || true
docker run -d --name deneb-web \
	--network host \
	--restart unless-stopped \
	--read-only \
	--cpus 0.5 --memory 128m --memory-swap 128m --pids-limit 128 \
	--log-opt max-size=10m --log-opt max-file=3 \
	deneb-web:v0 -static /app -listen "127.0.0.1:$port" -control "$control" -control-pin "$pin"
command -v curl >/dev/null || { sleep 2; docker ps --filter name=^deneb-web$; echo "no curl here to check it with"; exit 0; }
for i in 1 2 3 4 5 6 7 8 9 10; do
	curl -s -o /dev/null "http://127.0.0.1:$port/" && break
	sleep 1
done
docker ps --filter name=^deneb-web$ --format '{{.Names}} {{.Status}}'
docker logs --tail 5 deneb-web
app=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/")
api=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/v1/userview")
echo "the app: $app (want 200); the API through it: $api (want 401: reached the control server, not logged in)"
[ "$app" = 200 ] && [ "$api" = 401 ]
EOF
