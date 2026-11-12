#!/bin/sh
# Gets the Web version's certificate on hk-155 with certbot, the way the
# other names there get theirs: a DNS check through Cloudflare, with the
# credentials file already on the machine (CF_CREDENTIALS in the site
# settings). This script names the file and never reads it. certbot renews
# the certificate with the others; each renewal checks nginx's settings and
# reloads it.
#
#   deploy/web/cert-hk155.sh
#
# A DNS check needs no A record yet, and nothing of nginx: run it before
# nginx-hk155.sh install, which wants the certificate there.
set -eu
HOST=${HOST:-hk-155}
SITE=${SITE:-deploy/web/site.hk155.env}

cd "$(dirname "$0")/../.."
case $SITE in /*) ;; *) SITE=./$SITE ;; esac
. "$SITE"
: "${WEB_HOST:?in $SITE}" "${CF_CREDENTIALS:?in $SITE}"
CF=$CF_CREDENTIALS
ssh "$HOST" "
	set -e
	test -e $CF || { echo 'no Cloudflare credentials at $CF'; exit 1; }
	certbot certonly --dns-cloudflare --dns-cloudflare-credentials $CF \
		--dns-cloudflare-propagation-seconds 30 \
		-d $WEB_HOST --non-interactive --keep-until-expiring \
		--deploy-hook 'nginx -t && systemctl reload nginx'
	ls -l /etc/letsencrypt/live/$WEB_HOST/"
