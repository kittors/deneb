# deneb-front in an empty image: one static binary, so building it pulls
# nothing. Build the binary first with deploy/front/build.sh. Its address
# and the control server's come from deploy/front/deploy.sh.
FROM scratch
ARG TARGETARCH=amd64
COPY bin/${TARGETARCH}/deneb-front /usr/local/bin/deneb-front
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/deneb-front"]
