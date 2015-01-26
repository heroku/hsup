SHELL = /bin/sh

.SUFFIXES:

osx:
	godep go build -v -o hsup ./cmd/hsup
	./build/on_osx.sh
