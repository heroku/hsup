package hsup

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/fsouza/go-dockerclient"
	"strconv"
)

type DockerDynoDriver struct {
	d *Docker
}

func (dd *DockerDynoDriver) Build(release *Release) error {
	if release.stack != "cedar-14" {
		return fmt.Errorf("only cedar-14 is supported, but the "+
			"application uses the %q stack", release.stack)
	}

	if err := dd.connectDocker(); err != nil {
		return err
	}

	stack := "heroku/cedar:14"
	si, err := dd.d.StackStat(stack)
	if err != nil {
		return err
	}
	if si == nil {
		log.Fatalln("Stack not found:", stack)
	}

	imageName, err := dd.d.BuildSlugImage(si, release)
	if err != nil {
		log.Fatalln("could not build image:", err)
	}
	log.Println("Built image successfully")

	release.imageName = imageName
	return nil
}

func (dd *DockerDynoDriver) Start(ex *Executor) error {
	hs := Startup{
		App: AppSerializable{
			Version: ex.Release.version,
			Name:    ex.Release.appName,
			Env:     ex.Release.config,
			Stack:   ex.Release.stack,
			Processes: []FormationSerializable{
				{
					FArgs:     ex.Args,
					FQuantity: 1,
					FType:     ex.ProcessType,
				},
			},
			LogplexURL: ex.logplexURLString(),
		},
		OneShot:     true,
		StartNumber: ex.ProcessID,
		Action:      Start,
		Driver:      &AbsPathDynoDriver{},
	}

	// attach a timestamp as some extra entropy because container names must be
	// unique
	name := fmt.Sprintf("%v.%v", ex.Name(), time.Now().Unix())
	vols := make(map[string]struct{})
	for _, inside := range ex.Binds {
		vols[inside] = struct{}{}
	}

	container, err := dd.d.c.CreateContainer(docker.CreateContainerOptions{
		Name: name,
		Config: &docker.Config{
			Cmd:          []string{"setuidgid", "dyno", "/hsup"},
			Env:          []string{"HSUP_CONTROL_GOB=" + hs.ToBase64Gob()},
			Image:        ex.Release.imageName,
			Volumes:      vols,
			ExposedPorts: map[docker.Port]struct{}{docker.Port("5000/tcp"): {}},
		},
	})
	if err != nil {
		log.Fatalln("could not create container:", err)
	}
	ex.container = container

	err = dd.d.c.StartContainer(ex.container.ID, &docker.HostConfig{
		Binds:           ex.bindPairs(),
		PublishAllPorts: true,
	})
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

	exposed := ex.container.NetworkSettings.Ports[docker.Port("5000/tcp")]
	ex.IPAddress = exposed[0].HostIP
	ex.Port, _ = strconv.Atoi(exposed[0].HostPort)

	return nil
}

func (dd *DockerDynoDriver) Wait(ex *Executor) (s *ExitStatus) {
	code, err := dd.d.c.WaitContainer(ex.container.ID)
	return &ExitStatus{Code: code, Err: err}
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
