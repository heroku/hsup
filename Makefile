#!/usr/bin/env make -f

SHELL = /bin/sh

.SUFFIXES:

.PHONY: all clean deb deb-local docker-images

# go build vars
tempdir        := $(shell mktemp -d 2>/dev/null || mktemp -d -t 'hsup.go')
gopkg          := $(tempdir)/pkg
gosrc          := src/github.com/heroku/hsup

# deb build vars
packagename    := hsup
version        := 0.0.8
buildpath      := $(shell pwd)/deb
controldir     := $(buildpath)/DEBIAN
installpath    := $(buildpath)/usr/bin

ifdef TRAVIS_COMMIT
version := $(version)-$(TRAVIS_BRANCH)~git~$(TRAVIS_COMMIT)
endif

define DEB_CONTROL
Package: $(packagename)
Version: $(version)
Architecture: amd64
Maintainer: "Heroku Dogwood" <dogwood@heroku.com>
Section: heroku
Priority: optional
Description: Heroku dyno supervisor
endef
export DEB_CONTROL

all: hsup-linux-amd64

clean:
	rm -f $(packagename)*.deb
	rm -f hsup
	rm -f hsup-linux-amd64

hsup:
	godep go build -v -o hsup ./cmd/hsup

hsup-linux-amd64: hsup docker-images
	mkdir -p -m 0755 $(gopkg)
	docker run -it --rm -v $$GOPATH/$(gosrc):/go/$(gosrc) -v $(gopkg):/go/$(gosrc)/Godeps/_workspace/pkg golang:1.4.1 \
	    /go/$(gosrc)/build/in_docker.sh
	rm -rf $(tempdir)
	install $$GOPATH/$(gosrc)/hsup-linux-amd64 $$GOPATH/bin/

deb-local: hsup
	mkdir -p -m 0755 $(controldir)
	echo "$$DEB_CONTROL" > $(controldir)/control
	mkdir -p $(installpath)
	install hsup $(installpath)/$(packagename)
	dpkg-deb -z9 -Zxz --build deb . && install -m 0666 ./$(packagename)*.deb deb/

deb: all
	mkdir -p -m 0755 $(controldir)
	echo "$$DEB_CONTROL" > $(controldir)/control
	mkdir -p $(installpath)
	install $$GOPATH/bin/hsup-linux-amd64 $(installpath)/$(packagename)
	docker run -it --rm -v $(buildpath):/go/deb golang:1.4 \
	    sh -c 'dpkg-deb --build deb . && install -m 0666 ./$(packagename)*.deb deb/'
	cp $(buildpath)/$(packagename)*.deb .
	rm -rf $(buildpath)

# Assumes docker (or boot2docker on OSX) is installed, working and running
docker-images:
	docker pull golang:1.4.1

