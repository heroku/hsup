# hsup [DEPRECATED]

_This project is officially retired and no longer maintained._

Supervises processes that are configured in a Heroku-esque way.

`hsup` can poll the Heroku API directly to obtain releases,
configuration, and execution information.  `hsup` can also watch a
local directory that injects similar information.

The execution is performed with a chosen "dyno driver":

* The default dyno driver, `simple`, downloads and refreshes the environment
  only.
* The `docker` dyno driver both obtains the environment and executable code and
  runs it interposed on the `heroku/cedar:14` image.
* The `libcontainer` driver is similar to the Docker driver, but runs containers
  in foreground, on top of Heroku official (read only) stack images. It needs to
  be executed as root (e.g.: `sudo`) and only works on Linux machines. See notes
  about running it in Docker (below) for nested (hsup-in-docker) support and
  execution on any host with Docker installed (e.g.: boot2docker).

Usage:

``` sh
hsup COMMAND [options] [args...]
```

Where `COMMAND` is on one:

* `run`: Run a command with an app's environment.
* `start`: Start a process type as defined in an app's `Procfile`.

Example:

``` sh
godep go install ./...
export DOCKER_HOST=unix:///var/run/docker.sock
export HEROKU_ACCESS_TOKEN=...

# start default process types inside Docker
hsup start -d docker start -a simple-brandur

# run command as subprocess
hsup run -d simple -a simple-brandur -- echo "hello"
```

Example using a directory:

```sh
godep go install ./...
export HSUP_CONTROL_DIR=/tmp/supctl
mkdir -p "$HSUP_CONTROL_DIR"
echo '{
    "Version": 1,
    "Env": {
        "NAME": "CONTENTS"
    },
    "Slug": "sample-slug.tgz",
    "Stack": "cedar-14",
    "Processes": [
        {
            "Args": ["./web-server", "arg"],
            "Quantity": 2,
            "Type": "web"
        },
        {
            "Args": ["./worker", "arg"],
            "Quantity": 2,
            "Type": "worker"
        }
    ]
}' > "$HSUP_CONTROL_DIR"/new
hsup run '/usr/bin/printenv'

# Note that after verifying the input, the file is moved to "loaded".
# Writing new "new" files is how updates can be issued.
ls "$HSUP_CONTROL_DIR"
```

## Running the libcontainer driver within Docker

If you are using boot2docker, do the necessary preparation to expand the
available disk space for hsup data:

```sh-session
$ make boot2docker-init
```

Containers (hsup/libcontainer) inside containers (docker). Inception!

```sh-session
$ docker build -t hsup .
$ docker run --privileged -it hsup
# will run /usr/bin/printenv, see docker/example.json for details
```

The docker container by default runs `hsup start --oneshot`. For a custom hsup
command, use:

```sh-session
$ docker run --privileged -it hsup run bash
(dyno console) ~ $
```

Stack images will be downloaded for each new fresh container. It is a good idea
to share a common stack image directory between all containers to avoid
downloading them every time:

```sh-session
$ docker run --privileged -v /var/lib/hsup:/var/lib/hsup -it hsup
```

A custom hsup control dir can be injected as a docker volume, in case custom
json control files are required:

```sh-session
$ docker run --privileged -v /tmp/supctl:/etc/hsup -v /tmp/stacks:/var/lib/hsup/stacks -it hsup
(/tmp/supctl/new or /tmp/supctl/loaded will be used as the control file)
```

## Automated functional tests

If you are using boot2docker (check the section above for details):

```sh-session
$ make boot2docker-init
```

To run several functional tests against a `hsup` binary:

```sh-session
$ godep go test ./ftest -driver docker -hsup <path-to-compiled-hsup-binary>
```

Different drivers (`libcontainer`, `simple`) can be specified with the `driver`
flag, but note that the libcontainer driver requires `root` privileges:

```sh-session
$ sudo -E $(which godep) go test ./ftest -driver libcontainer -hsup <path-to-hsup-binary>
```

All tests can also be executed inside docker containers (see the
hsup-inside-docker section above) with:

```sh-session
$ make ftest
runs libcontainer driver tests by default

$ make ftest driver=docker
specify a custom driver

$ make ftest-libcontainer
libcontainer driver tests...

$ make ftest-docker
docker driver tests...

$ make ftest-simple
simple driver tests...
```

## Driver specific configuration

Some drivers accept custom configuration via ENV.

### Docker

* `DOCKER_HOST`
* `DOCKER_CERT_PATH`
* `DOCKER_IMAGE_CACHE`: when set, docker images are only built once per release.

### Libcontainer

* `LIBCONTAINER_DYNO_SUBNET`: a CIDR block to allocate dyno subnets (of size
  /30) from. It is `172.16.0.0/12` (RFC1918) by default when not set.
* `LIBCONTAINER_DYNO_EXTRA_INTERFACE`: interface on the host to inject into the
  dyno (currently as a [ipvlan][ipvlan] subinterface), together with its IP address
  CIDR in the format: `hostIFName:IP/Mask`. Eg.: `eth1:10.0.0.10/24`.
* `LIBCONTAINER_DYNO_EXTRA_ROUTES`: extra routes to add to the dyno network
  namespace main routing table. Format: `IP/Mask:Gateway:IF,IP/Mask:Gateway,IF,...`,
  example: `10.0.0.0/8:10.1.1.1:eth1,192.168.0.0/24:default:eth0`. The special
  value `default` can be used as the gateway, and will be replaced with the
  dyno's default route gateway at runtime.
* `LIBCONTAINER_DYNO_UID_MIN` and `LIBCONTAINER_DYNO_UID_MAX`: Linux UIDs to use
  for each dyno. It also defines the maximum number of allowed dynos, as each
  dyno gets a unique UID per box. To avoid reusing subnets (IPs), make sure that
  `(maxUID - minUID) <= /30 subnets that LIBCONTAINER_DYNO_SUBNET can provide`.
  `172.17.0.0/16` can provide `2 ** (30-16)` = **16384** subnets of size /30. In
  this case, to avoid subnets being reused, make sure that `(maxUID - minUID) <= 16384`.

[ipvlan]: https://github.com/torvalds/linux/blob/master/Documentation/networking/ipvlan.txt
