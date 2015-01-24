SHELL = /bin/sh

.SUFFIXES:

osx:
	go build -v -o hsup ./cmd/hsup
	./build/on_osx.sh
