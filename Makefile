SHELL = /bin/sh

.SUFFIXES:

osx:
	go build -tags daemon -v -o hsup ./cmd/hsup
	./build/on_osx.sh
