# M5's Linux machines for the colima topology: Debian or Ubuntu with
# systemd as PID 1 and systemd-resolved (libnss-resolve puts it in the
# hosts line: Docker's own /etc/resolv.conf stays), for the deneb packages
# and kernel mode. Run privileged, with tmpfs on /run.
ARG BASE=debian:stable-slim
FROM ${BASE}
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN apt-get update && apt-get install -y --no-install-recommends \
		systemd systemd-sysv systemd-resolved libnss-resolve dbus \
		iproute2 iputils-ping curl ca-certificates procps \
	&& apt-get clean && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
