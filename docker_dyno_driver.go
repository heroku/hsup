package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/fsouza/go-dockerclient"
)

type DockerDynoDriver struct {
	d     *Docker
	state DynoState

	cmd       *exec.Cmd
	container *docker.Container
	waiting   chan error
}

func NewDockerDynoDriver() *DockerDynoDriver {
	return &DockerDynoDriver{}
}

func (dd *DockerDynoDriver) State() DynoState {
	return dd.state
}

func (dd *DockerDynoDriver) Start(b *Bundle) error {
	if dd.d == nil {
		dd.d = &Docker{}
		if err := dd.d.Connect(); err != nil {
			dd.d = nil
			return err
		}
	}

	si, err := dd.d.StackStat("cedar-14")
	if err != nil {
		return err
	}

	log.Printf("StackImage %+v", si)
	imageName, err := dd.d.BuildSlugImage(si, b)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Built image successfully")

	// Fill environment vector from Heroku configuration.
	env := make([]string, 0)
	for k, v := range b.config {
		env = append(env, k+"="+v)
	}

	var cmd []string
	if b.formation != nil {
		cmd = []string{b.formation.Command}
	} else {
		cmd = b.argv
	}

	dd.container, err = dd.d.c.CreateContainer(docker.CreateContainerOptions{
		Name: fmt.Sprintf("%v-%v", imageName, int32(time.Now().Unix())),
		Config: &docker.Config{
			Cmd:   cmd,
			Env:   env,
			Image: imageName,
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	err = dd.d.c.StartContainer(dd.container.ID, &docker.HostConfig{})
	if err != nil {
		return err
	}

	dd.state = Started

	go dd.d.c.Logs(docker.LogsOptions{
		Container:    dd.container.ID,
		Stdout:       true,
		Stderr:       true,
		Follow:       true,
		OutputStream: os.Stdout,
	})

	return nil
}

func (dd *DockerDynoDriver) Stop() error {
	err := dd.d.c.StopContainer(dd.container.ID, 10)
	dd.state = Stopped
	return err
}
