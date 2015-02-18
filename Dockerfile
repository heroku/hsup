FROM ubuntu:14.04
MAINTAINER dogwood@heroku.com

RUN apt-get update && \
    apt-get install -y curl apt-transport-https software-properties-common \
        make git mercurial jq gcc \
        bridge-utils iptables && \
    apt-get clean

COPY ./Godeps/Godeps.json /tmp/
COPY docker/buildpack_prep.sh /tmp/
RUN /tmp/buildpack_prep.sh && rm /tmp/buildpack_prep.sh

RUN mkdir -p /app
RUN mkdir -p /var/lib/hsup/stacks
RUN mkdir -p /etc/hsup/containers/sockets
COPY docker/example.json /etc/hsup/new

VOLUME /var/lib/hsup
VOLUME /etc/hsup

ADD . /app
WORKDIR /app
RUN /var/lib/buildpack/bin/compile "/app" "/var/cache/buildpack"

COPY docker/hsup-in-docker /sbin/hsup-in-docker
ENV HSUP_CONTROL_DIR /etc/hsup
ENTRYPOINT ["/sbin/hsup-in-docker", "/app/bin/hsup", "-d", "libcontainer", "-s", "/etc/hsup/containers/sockets/hsup.sock"]
CMD ["start", "--oneshot"]
