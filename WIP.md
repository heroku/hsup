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
$ go build &&
  cp hsup tmp/root &&
  sudo chroot tmp/root env HSUP_ABSPATH_UID=$(id -u) \
	HSUP_ABSPATH_BASE=tmp/root HEROKU_ACCESS_TOKEN=[REDACTED] \
	/hsup run printenv -d abspath -a [MYAPP]
```
