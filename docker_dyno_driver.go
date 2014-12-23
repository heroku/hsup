package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/fsouza/go-dockerclient"
)

type DockerDynoDriver struct {
	d     *Docker
	state DynoState
	waiting          chan error
}

func (dd *DockerDynoDriver) Build(release *Release) error {
	if err := dd.connectDocker(); err != nil {
		return err
	}

	si, err := dd.d.StackStat("cedar-14")
	if err != nil {
		return err
	}

	imageName, err := dd.d.BuildSlugImage(si, release)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Built image successfully")

	release.imageName = imageName
	return nil
}

func (dd *DockerDynoDriver) State() DynoState {
	return dd.state
}

func (dd *DockerDynoDriver) Start(release *Release, ex *Executor) error {
	ex.containers = make([]*docker.Container, 0)

	// Fill environment vector from Heroku configuration.
	env := make([]string, 0)
	for k, v := range release.config {
		env = append(env, k+"="+v)
	}

	container, err := dd.d.c.CreateContainer(docker.CreateContainerOptions{
		Name: fmt.Sprintf("%v-%v", release.imageName, int32(time.Now().Unix())),
		Config: &docker.Config{
			Cmd:   ex.Args(),
			Env:   env,
			Image: release.imageName,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	ex.containers = append(ex.containers, container)

	for _, container := range ex.containers {
		err = dd.d.c.StartContainer(container.ID, &docker.HostConfig{})
		if err != nil {
			log.Fatal(err)
		}

		go dd.d.c.Logs(docker.LogsOptions{
			Container:    container.ID,
			Stdout:       true,
			Stderr:       true,
			Follow:       true,
			OutputStream: os.Stdout,
		})
	}

	dd.state = Started
	return nil
}

func (dd *DockerDynoDriver) Stop(ex *Executor) error {
	for _, container := range ex.containers {
		err := dd.d.c.StopContainer(container.ID, 10)
		return err
	}

	// @todo: need to be move this onto an executor instead of a driver
	dd.state = Stopped
	return nil
}

func (dd *DockerDynoDriver) connectDocker() error {
	if dd.d == nil {
		dd.d = &Docker{}
		if err := dd.d.Connect(); err != nil {
			dd.d = nil
			return err
		}
	}

	return nil
}
