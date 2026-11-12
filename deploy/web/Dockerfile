# The Web version in an empty image: deneb-web and the built app, nothing
# else, so building it pulls nothing. Build both first with
# deploy/web/build.sh, then from deploy/web/:
#
#   docker build -t deneb-web:v0 .
#
# BuildKit sets TARGETARCH; the default is for the classic builder. The
# image has no shell or curl: deploy-hk155.sh checks it from the host.
FROM scratch
ARG TARGETARCH=amd64
COPY bin/${TARGETARCH}/deneb-web /usr/local/bin/deneb-web
COPY app/ /app/
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/deneb-web"]
CMD ["-static", "/app"]
