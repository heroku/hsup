#!/usr/bin/env bash

cd /go/src/github.com/heroku/hsup
godep go build -v -o hsup-linux-amd64 ./cmd/hsup
