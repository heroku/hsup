# WIP

`hsup` is a platform for experimentation.  This file, to avoid
overwhelming the README for casual reading, is for notes in testing
and developing new features.

## docker image caching

Docker images can be cached and re-used for each (app, release) tuple. To enable
caching, use the `DOCKER_IMAGE_CACHE` env var:

```sh-session
$ export DOCKER_IMAGE_CACHE=1 HSUP_CONTROL_DIR=/tmp/supctl
$ hsup start -a myapp web=1
```

This avois slugs being downloaded all the time, but beware that the cache needs
to be invalidated manually, and an hsup binary will be baked into the cached
image the first time it's built. If you need to update hsup inside the image,
invalidade the cache manually by deleting the image in docker directly, or
running hsup again with the cache disabled.

## abspath driver

`hsup` is learning to deal with environments set up with absolute
paths, e.g. chroot environments and containers.

Example:

```sh-session
# Sets up a minimal "Cedar-14" chroot in 'tmp/root'
$ ./util/abspath-model

# Build, copy the hsup binary into the chroot's /tmp, and execute it.
$ export HEROKU_ACCESS_TOKEN=[REDACTED]
$ export HSUP_APP=[REDACTED]
$ godep go build &&
  GOOS=linux GOARCH=amd64 go build -o hsup-linux-amd64 &&
  cp hsup tmp/root &&
  sudo chroot tmp/root env \
    "HEROKU_ACCESS_TOKEN=$HEROKU_ACCESS_TOKEN" \
    /hsup run printenv -d abspath -a "$HSUP_APP"
```
