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

	releaseStates    map[*Release]*DockerDynoDriverReleaseStateFactoryFactoryFactory
	cmd              *exec.Cmd
	executableStates map[Executable]*DockerDynoDriverExecutableStateFactoryFactorFactory
	waiting          chan error
}

type DockerDynoDriverReleaseStateFactoryFactoryFactory struct {
	imageName string
}

type DockerDynoDriverExecutableStateFactoryFactorFactory struct {
	containers []*docker.Container
}

func NewDockerDynoDriver() *DockerDynoDriver {
	return &DockerDynoDriver{
		executableStates: make(map[Executable]*DockerDynoDriverExecutableStateFactoryFactorFactory),
		releaseStates:    make(map[*Release]*DockerDynoDriverReleaseStateFactoryFactoryFactory),
	}
}

func (dd *DockerDynoDriver) Build(release *Release) error {
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

	imageName, err := dd.d.BuildSlugImage(si, release)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Built image successfully")

	dd.releaseStates[release] = &DockerDynoDriverReleaseStateFactoryFactoryFactory{
		imageName: imageName,
	}

	return nil
}

func (dd *DockerDynoDriver) State() DynoState {
	return dd.state
}

func (dd *DockerDynoDriver) Start(release *Release, ex Executable) error {
	if dd.d == nil {
		dd.d = &Docker{}
		if err := dd.d.Connect(); err != nil {
			dd.d = nil
			return err
		}
	}

	releaseState, ok := dd.releaseStates[release]
	if !ok {
		log.Fatal("Release state not found for: " + release.Name())
	}

	executableState := &DockerDynoDriverExecutableStateFactoryFactorFactory{
		containers: make([]*docker.Container, 0),
	}

	// Fill environment vector from Heroku configuration.
	env := make([]string, 0)
	for k, v := range release.config {
		env = append(env, k+"="+v)
	}

	container, err := dd.d.c.CreateContainer(docker.CreateContainerOptions{
		Name: fmt.Sprintf("%v-%v", releaseState.imageName, int32(time.Now().Unix())),
		Config: &docker.Config{
			Cmd:   ex.Args(),
			Env:   env,
			Image: releaseState.imageName,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	executableState.containers = append(executableState.containers, container)

	for _, container := range executableState.containers {
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

func (dd *DockerDynoDriver) Stop() error {
	for _, exState := range dd.executableStates {
		for _, container := range exState.containers {
			err := dd.d.c.StopContainer(container.ID, 10)
			return err
		}
	}

	dd.state = Stopped
	return nil
}
