# The watchdog in an empty image: one static binary, so building it pulls
# nothing. Build the binary first with deploy/watchdog/build.sh. It listens
# on nothing; its file and the host's CA bundle are mounted read-only
# (deploy/watchdog/deploy.sh).
FROM scratch
ARG TARGETARCH=amd64
COPY bin/${TARGETARCH}/deneb-watchdog /usr/local/bin/deneb-watchdog
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/deneb-watchdog"]
CMD ["-config", "/etc/deneb-watchdog/watch.json"]
