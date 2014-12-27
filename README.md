# hsup

Queries the Heroku API, downloads environment variables, and then runs
a process with arguments.

Usage:

``` sh
hsup COMMAND [options]
```

Where `COMMAND` is on one:

* `run`: Run a command with an app's environment.
* `start`: Start a process type as defined in an app's `Procfile`.

Example:

``` sh
go build

export DOCKER_HOST=unix:///var/run/docker.sock
export HEROKU_ACCESS_TOKEN=...

# start default process types inside Docker
hsup start -d docker -c 2 -a simple-brandur

# run command as subprocess
hsup run -d simple -c 2 -a simple-brandur echo "hello"
```
