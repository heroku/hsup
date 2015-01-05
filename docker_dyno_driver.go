package main

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/fsouza/go-dockerclient"
)

type DockerDynoDriver struct {
	d *Docker
}

func (dd *DockerDynoDriver) Build(release *Release) error {
	if err := dd.connectDocker(); err != nil {
		return err
	}

	stack := "heroku/cedar:14"
	si, err := dd.d.StackStat(stack)
	if err != nil {
		return err
	}
	if si == nil {
		log.Fatalf("Stack not found = %v\n", stack)
	}

	imageName, err := dd.d.BuildSlugImage(si, release)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Built image successfully")

	release.imageName = imageName
	return nil
}

func (dd *DockerDynoDriver) Start(ex *Executor) error {
	// Fill environment vector from Heroku configuration.
	var env []string
	for k, v := range ex.release.config {
		env = append(env, k+"="+v)
	}

	// attach a timestamp as some extra entropy because container names must be
	// unique
	name := fmt.Sprintf("%v.%v", ex.Name(), time.Now().Unix())
	container, err := dd.d.c.CreateContainer(docker.CreateContainerOptions{
		Name: name,
		Config: &docker.Config{
			Cmd:   ex.args,
			Env:   env,
			Image: ex.release.imageName,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	ex.container = container

	err = dd.d.c.StartContainer(ex.container.ID, &docker.HostConfig{})
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

	return nil
}

func (dd *DockerDynoDriver) Wait(ex *Executor) error {
	_, err := dd.d.c.WaitContainer(ex.container.ID)
	return err
}

func (dd *DockerDynoDriver) Stop(ex *Executor) error {
	log.Println("Stopping container for", ex.Name())
	dd.d.c.KillContainer(docker.KillContainerOptions{
		ID:     ex.container.ID,
		Signal: docker.Signal(syscall.SIGTERM)})
	return dd.d.c.StopContainer(ex.container.ID, 10)
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
