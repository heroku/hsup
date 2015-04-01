# hsup

Supervises processes that are configured in a Heroku-esque way.

`hsup` can poll the Heroku API directly to obtain releases,
configuration, and execution information.  `hsup` can also watch a
local directory that injects similar information.

The execution is performed with a chosen "dyno driver".  The default
dyno driver, `simple`, downloads and refreshes the environment only.
There is also a `docker` dyno driver that both obtains the environment
and executable code and runs it interposed on the `heroku/cedar:14`
image.

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
$ mkdir /tmp/stacks
$ docker run --privileged -v /tmp/stacks:/var/lib/hsup/stacks -it hsup
...
$ docker run --privileged -v /tmp/stacks:/var/lib/hsup/stacks -it hsup
(stack images will be cached)
...
```

A custom hsup control dir can be injected as a docker volume, in case custom
json control files are required:

```sh-session
$ docker run --privileged -v /tmp/supctl:/etc/hsup -v /tmp/stacks:/var/lib/hsup/stacks -it hsup
(/tmp/supctl/new or /tmp/supctl/loaded will be used as the control file)
```

## Automated functional tests

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

