#!/usr/bin/env bash

cd /go/src/github.com/heroku/hsup
go build -o godep-linux-amd64 github.com/tools/godep
./godep-linux-amd64 go build -v -o hsup-linux-amd64 ./cmd/hsup
