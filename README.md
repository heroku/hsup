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

## Running within docker

```sh-session
$ docker build -t hsup .
$ mkdir t
# place `new` in t as described above
$ docker run --privileged -it -v `pwd`/t:/ctl -e HSUP_CONTROL_DIR=/ctl hsup bin/run-in-docker -d libcontainer --oneshot start
```
