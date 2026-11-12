#!/bin/sh
# Checks the Web version's whole chain on this machine, before a deploy:
# nginx with the site file nginx-hkspare.sh makes (in Docker,
# nginx:1.27-alpine, a self-signed certificate) → deneb-web → deneb-control
# -dev, as on hk-spare and hk-155 but all here, under deneb.example.com, the
# control server trusting X-Real-IP from 127.0.0.1 where deneb-web runs. Run
# deploy/web/build.sh first (for the app).
#
#   deploy/web/check-local.sh
#
# What to see: the app 200, the API 401 then 200 logged in, a login with no
# Sec-Fetch-Site 200, the cookie Secure, the stream's first frame, the
# control server seeing nginx's address (not the X-Real-IP the client
# made up, nor 127.0.0.1), and its own invite link a 302 to the Web version.
# Ports 47981-47983 on 127.0.0.1; everything goes under deploy/web/bin/e2e.
set -eu
cd "$(dirname "$0")/../.."
root=$PWD
e=$root/deploy/web/bin/e2e
rm -rf "$e"; mkdir -p "$e/live" "$e/data"
go build -o "$e/deneb-control" ./cmd/deneb-control
go build -o "$e/deneb-web" ./cmd/deneb-web
"$e/deneb-control" -dev -listen 127.0.0.1:47981 -data "$e/data" -log-dir off -public-url http://127.0.0.1:47981 \
	-web-url https://deneb.example.com -trust-proxy 127.0.0.1 > "$e/control.log" 2>&1 &
cpid=$!
"$e/deneb-web" -listen 127.0.0.1:47982 -static "$root/deploy/web/app" -control http://127.0.0.1:47981 > "$e/web.log" 2>&1 &
wpid=$!
trap 'kill $cpid $wpid 2>/dev/null; docker rm -f deneb-e2e-nginx >/dev/null 2>&1 || true' EXIT
curl -s --retry 30 --retry-connrefused --retry-delay 1 -o /dev/null http://127.0.0.1:47981/healthz
curl -s --retry 30 --retry-connrefused --retry-delay 1 -o /dev/null http://127.0.0.1:47982/

printf 'WEB_HOST=deneb.example.com\nWEB_PORT=47982\nNGINX_LISTEN_80="80"\nNGINX_LISTEN_443="443 ssl;[::]:443 ssl"\n' > "$e/site.env"
SITE="$e/site.env" deploy/web/nginx-hkspare.sh show | sed 's|http://127.0.0.1:47982|http://host.docker.internal:47982|' > "$e/site.conf"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=deneb.example.com \
	-keyout "$e/live/privkey.pem" -out "$e/live/fullchain.pem" 2>/dev/null
docker run -d --name deneb-e2e-nginx -p 127.0.0.1:47983:443 \
	-v "$e/site.conf:/etc/nginx/conf.d/site.conf:ro" -v "$e/live:/etc/letsencrypt/live/deneb.example.com:ro" \
	nginx:1.27-alpine >/dev/null
B="curl -sk --resolve deneb.example.com:47983:127.0.0.1"
$B --retry 30 --retry-all-errors --retry-delay 1 -o /dev/null https://deneb.example.com:47983/

echo "the app: $($B -o /dev/null -w '%{http_code}' https://deneb.example.com:47983/)"
echo "an invite page: $($B -o /dev/null -w '%{http_code}' https://deneb.example.com:47983/i/sometoken)"
echo "the API, not logged in: $($B -o /dev/null -w '%{http_code}' https://deneb.example.com:47983/api/v1/userview)"
# An old browser: no Sec-Fetch-Site; and an X-Real-IP of its own, which nginx must overwrite.
st=$($B -D "$e/h.txt" -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -H 'Origin: https://deneb.example.com' \
	-H 'X-Real-IP: 198.51.100.66' -d '{"email":"kittors@deneb.test","code":"000000"}' https://deneb.example.com:47983/api/v1/login)
echo "login, no Sec-Fetch-Site: $st"
grep -i '^set-cookie' "$e/h.txt" | sed -E 's/=[^;]*;/=<token>;/' | tr -d '\r'
cookie=$(grep -i '^set-cookie' "$e/h.txt" | head -1 | tr -d '\r' | sed -E 's/^[^:]+: ([^;]+);.*/\1/')
echo "the view, logged in: $($B -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" https://deneb.example.com:47983/api/v1/userview)"
printf 'the stream'"'"'s first frame within 3 s: '
$B -m 3 -H "Cookie: $cookie" https://deneb.example.com:47983/api/v1/stream 2>/dev/null | head -c 60 || true
echo
echo "the control server saw: $(grep -o 'web login: .*' "$e/control.log" | head -1)"
curl -s -o /dev/null -w 'the control server'"'"'s own invite link: %{http_code} to %{redirect_url}\n' http://127.0.0.1:47981/i/sometoken
