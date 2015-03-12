#!/bin/bash

set -eo pipefail

/var/lib/buildpack/bin/compile "$PWD" "/var/cache/buildpack"
