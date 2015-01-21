#!/usr/bin/env bash
set -e

# Assumes boot2docker is installed, working, running, and your current
# env points at it correctly.
docker pull golang:1.4

docker run -it --rm -v $GOPATH:/go -w /usr/src/myapp golang:1.4 \
    /go/src/github.com/heroku/hsup/build/in_docker.sh
