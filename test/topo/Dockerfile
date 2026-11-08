# The colima topology's one image: the engine (test build), the control
# server, the relay, and the tools the NAT routers need.
FROM alpine:latest
RUN apk add --no-cache iproute2 iptables curl jq
COPY bin/denebd bin/deneb-control bin/derper /usr/local/bin/
COPY node.sh /usr/local/bin/node.sh
RUN ln -s denebd /usr/local/bin/deneb
ENTRYPOINT ["/usr/local/bin/node.sh"]
