package main

import (
	"fmt"
	"log"

	"github.com/fsouza/go-dockerclient"
)

const (
	NeverStarted DynoState = iota
	Started
	Stopped
)

type DynoDriver interface {
	Build(*Release) error
	Start(*Executor) error
	Stop(*Executor) error
}

type DynoState int

type Release struct {
	appName string
	config  map[string]string
	slugUrl string
	version int

	// docker dyno driver properties
	imageName string
}

func (r *Release) Name() string {
	return fmt.Sprintf("%v-%v", r.appName, r.version)
}

type Executor struct {
	argv        []string
	dynoDriver  DynoDriver
	release     *Release
	state       DynoState
	processID   string
	processType string

	// docker dyno driver properties
	container *docker.Container
}

func (e *Executor) Name() string {
	return e.processType + "." + e.processID
}

func (e *Executor) Start() {
again:
	s := e.state
	switch s {
	case NeverStarted:
		fallthrough
	case Stopped:
		log.Printf("%v: starting\n", e.Name())
		err := e.dynoDriver.Start(e)
		if err != nil {
			log.Printf("process could not start with error: %v\n", err)
		}
		e.state = Started
		log.Printf("%v: started\n", e.Name())
	case Started:
		e.Stop()
		goto again
	default:
		log.Fatalf("BUG bad state: %v\n", s)
	}
}

func (e *Executor) Stop() {
	log.Printf("%v: stopping\n", e.Name())
	err := e.dynoDriver.Stop(e)
	if err != nil {
		log.Printf("%v: stopped with error: %v", e.Name(), err.Error())
	} else {
		log.Printf("%v: stopped\n", e.Name())
	}
	e.state = Stopped
}

func FindDynoDriver(name string) (DynoDriver, error) {
	switch name {
	case "simple":
		return &SimpleDynoDriver{}, nil
	case "docker":
		return &DockerDynoDriver{}, nil
	default:
		return nil, fmt.Errorf("could not locate driver. "+
			"specified by the user: %v", name)
	}
}
