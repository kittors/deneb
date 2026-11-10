# Deneb's relay in an empty image: one static binary and nothing else.
# Build the binary first with deploy/relay/build.sh, then from
# deploy/relay/:
#
#   docker build -t deneb-relay:v0 .
#
# Run it with the control server and, the first time, the registration
# token from relay.create (or deneb-control relay-add):
#
#   docker run -d --name deneb-relay --network host -v /opt/deneb-relay/data:/data \
#     -e DENEB_RELAY_TOKEN=deneb-relay-… deneb-relay:v0 \
#     -control https://203.0.113.1:47443 -control-pin sha256:… -data /data
#
# BuildKit sets TARGETARCH; the default is for the classic builder.
FROM scratch
ARG TARGETARCH=amd64
COPY bin/${TARGETARCH}/deneb-relay /usr/local/bin/deneb-relay
# /data keeps the relay's secret, certificate (whose pin devices trust) and
# DERP key. It is owned by nobody, whom the relay runs as, so a fresh
# volume is writable.
COPY --chown=65534:65534 skel/ /data/
USER 65534:65534
VOLUME /data
# DERP over HTTPS, and STUN.
EXPOSE 47445/tcp 47478/udp
# The image has no shell or curl: the binary checks itself, on whatever
# port it listens on (it notes it in /data).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD ["/usr/local/bin/deneb-relay", "-healthcheck", "-data", "/data"]
ENTRYPOINT ["/usr/local/bin/deneb-relay"]
CMD ["-data", "/data"]
