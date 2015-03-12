#!/bin/bash

set -e

# Install the Go buildpack and pre-populate its cache
mkdir -p /tmp/dummy-app/Godeps
mkdir -p /var/lib/buildpack
mkdir -p /var/cache/buildpack
git clone --depth 1 https://github.com/heroku/heroku-buildpack-go.git /var/lib/buildpack
chown -R app:app /var/cache/buildpack
echo "{ \"ImportPath\": \"dummy\", \"GoVersion\": \"$(</tmp/Godeps.json jq -r .GoVersion)\" }" > /tmp/dummy-app/Godeps/Godeps.json
chown -R app /tmp/dummy-app
sudo -u app /var/lib/buildpack/bin/compile "/tmp/dummy-app" "/var/cache/buildpack"
rm -rf /tmp/dummy-app /tmp/Godeps.json
