# The control server in an empty image: one static binary and nothing
# else, so building it pulls nothing. Build the binary first with
# deploy/control/build.sh, then from deploy/control/:
#
#   docker build -t deneb-control:v0 .
#
# BuildKit sets TARGETARCH; the default is for the classic builder.
FROM scratch
ARG TARGETARCH=amd64
# In /usr/local/bin, which docker exec has on its PATH, so the admin
# commands read: docker exec deneb-control deneb-control approve ...
COPY bin/${TARGETARCH}/deneb-control /usr/local/bin/deneb-control
# /data is where the database, the TLS key and the backups live. It is
# owned by nobody, whom the server runs as, so a fresh volume is writable.
COPY --chown=65534:65534 skel/ /data/
USER 65534:65534
VOLUME /data
EXPOSE 47443
# The image has no shell or curl: the binary checks itself. The port is the
# production one; a development container (-dev on 47080) runs with
# --no-healthcheck.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD ["/usr/local/bin/deneb-control", "-healthcheck", "https://127.0.0.1:47443/healthz"]
ENTRYPOINT ["/usr/local/bin/deneb-control"]
CMD ["-listen", ":47443", "-data", "/data", "-seed", "/data/seed.json"]
