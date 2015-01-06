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
go install
export DOCKER_HOST=unix:///var/run/docker.sock
export HEROKU_ACCESS_TOKEN=...

# start default process types inside Docker
hsup start -d docker -c 2 -a simple-brandur

# run command as subprocess
hsup run -d simple -c 2 -a simple-brandur echo "hello"
```

Example using a directory:

```sh
go install
export CONTROL_DIR=/tmp/supctl
mkdir -p "$CONTROL_DIR"
echo '{
    "Version": 1,
    "Env": {
        "NAME": "CONTENTS"
    },
    "Slug": "sample-slug.tgz",
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
}' > "$CONTROL_DIR"/new
hsup run '/usr/bin/printenv'

# Note that after verifying the input, the file is moved to "loaded".
# Writing new "new" files is how updates can be issued.
ls "$CONTROL_DIR"
```
