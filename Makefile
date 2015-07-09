#!/usr/bin/env make -f

SHELL = /bin/sh

.SUFFIXES:

.PHONY: all clean deb deb-local ftest ftest-libcontainer ftest-simple ftest-docker hsup-docker-container docker-images boot2docker-init

# go build vars
tempdir        := $(shell mktemp -d 2>/dev/null || mktemp -d -t 'hsup.go')
gopkg          := $(tempdir)/pkg
gosrc          := src/github.com/heroku/hsup

# deb build vars
packagename    := hsup
version        ?= 0.1.0
buildpath      := $(shell pwd)/deb
controldir     := $(buildpath)/DEBIAN
installpath    := $(buildpath)/usr/bin

# testing vars
driver         ?= libcontainer

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
	rm -rf Godeps/_workspace/pkg

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

ftest: hsup-docker-container
	docker run --privileged --cap-add=ALL \
	    -v /var/run/docker.sock:/run/docker.sock \
	    -v /var/lib/hsup/stacks:/var/lib/hsup/stacks \
	    -v /lib/modules:/lib/modules \
	    --entrypoint="/sbin/hsup-in-docker" hsup sh -c \
	    'mkdir -p /var/cache/buildpack/go1.4.1/go/src/github.com/heroku/ && \
	    ln -s /app /var/cache/buildpack/go1.4.1/go/src/github.com/heroku/hsup && \
	    env PATH=/var/lib/buildpack/linux-amd64/bin/:/var/cache/buildpack/go1.4.1/go/bin:$$PATH \
	    GOROOT=/var/cache/buildpack/go1.4.1/go \
	    godep go test $(GO_TEST_FLAGS) ./ftest -driver $(driver) -hsup /app/bin/hsup'

ftest-libcontainer: driver = libcontainer
ftest-libcontainer: ftest

ftest-docker: driver = docker
ftest-docker: ftest

ftest-simple: driver = simple
ftest-simple: ftest

# Assumes docker (or boot2docker on OSX) is installed, working and running
docker-images:
	docker pull golang:1.4.1

hsup-docker-container:
	docker build -t hsup .

boot2docker-init:
	boot2docker ssh "sudo sh -c \
	    'mkdir -p /mnt/sda1/var/lib/hsup && \
	    ln -sf /mnt/sda1/var/lib/hsup /var/lib/hsup'"
