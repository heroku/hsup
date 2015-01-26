## building / using the docker driver on osx

* Have boot2docker up and running, `docker ps` should be working locally.
* run `make`

this will pull a linux based container with go 1.4 installed, and build an 'hsup-linux-amd64' binary that will get picked up when using the docker driver.
