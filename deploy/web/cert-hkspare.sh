#!/bin/sh
# Gets the Web version's certificate on hk-spare with certbot, the way the
# other names there get theirs: a DNS check through Cloudflare, with the
# credentials file already on the machine (CF_CREDENTIALS in the site
# settings). This script names the file and never reads it. certbot renews
# the certificate with the others; each renewal checks nginx's settings and
# reloads it.
#
#   deploy/web/cert-hkspare.sh
#
# A DNS check needs nothing of nginx: run it before nginx-hkspare.sh install,
# which wants the certificate there.
set -eu
HOST=${HOST:-hk-spare}
SITE=${SITE:-deploy/web/site.hkspare.env}

cd "$(dirname "$0")/../.."
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_HOST:?in $SITE}" "${CF_CREDENTIALS:?in $SITE}"
ssh "$HOST" sh -s -- "$WEB_HOST" "$CF_CREDENTIALS" <<'EOF'
set -eu
host=$1 cf=$2
test -e "$cf" || { echo "no Cloudflare credentials at $cf"; exit 1; }
certbot certonly --dns-cloudflare --dns-cloudflare-credentials "$cf" \
	--dns-cloudflare-propagation-seconds 30 \
	-d "$host" --non-interactive --keep-until-expiring \
	--deploy-hook 'nginx -t && systemctl reload nginx'
ls -l "/etc/letsencrypt/live/$host/"
EOF
