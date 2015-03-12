FROM ubuntu:14.04
MAINTAINER dogwood@heroku.com

RUN apt-get update && apt-get install -y curl apt-transport-https software-properties-common make git mercurial jq gcc bridge-utils iptables && apt-get clean

RUN useradd -M app -d /app

COPY ./Godeps/Godeps.json /tmp/
COPY docker/buildpack_prep.sh /tmp/
RUN /tmp/buildpack_prep.sh && rm /tmp/buildpack_prep.sh

RUN mkdir -p /etc/container_environment
RUN mkdir -p /var/lib/hsup/stacks
RUN mkdir -p /etc/hsup
RUN chown app:app /etc/hsup

RUN echo > /etc/bash.bashrc
ADD . /app
RUN chown -R app:app /app
ADD docker/profile.sh /etc/profile

WORKDIR /app

COPY docker/dind /sbin/dind
ENTRYPOINT ["/sbin/dind"]
CMD ["agent"]

RUN sudo -u app -i docker/build.sh
