#!/usr/bin/env bash

cd /go/src/github.com/heroku/hsup
go get github.com/tools/godep
godep go install ./...
cp /go/bin/hsup hsup-linux-amd64
rm -rf Godeps/_workspace/pkg/*
