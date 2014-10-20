# hsup

Queries the Heroku API, downloads environment variables, and then runs
a process with arguments.

Usage:

    hsup [app] [executable] [args ...]

Example:

    hsup www bin/web -p $PORT
