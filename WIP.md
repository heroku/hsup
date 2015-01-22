# WIP

`hsup` is a platform for experimentation.  This file, to avoid
overwhelming the README for casual reading, is for notes in testing
and developing new features.

## abspath driver

`hsup` is learning to deal with environments set up with absolute
paths, e.g. chroot environments and containers.

Example:

```
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

## libcontainer driver

It works (with a few caveats listed below), but requires `root` to be used:

```
$ godep go install ./... && sudo env HSUP_CONTROL_DIR=/tmp/hspctl \
  hsup run -d libcontainer '/bin/bash'
```

Caveats:

* it must be build with `godep`
* no support for local slugs
* no privilege dropping, containers still run as root
* no networking
