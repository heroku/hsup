package main

import (
	"fmt"
	"log"

	"github.com/cyberdelia/heroku-go/v3"
	"github.com/fsouza/go-dockerclient"
)

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
	argv       []string
	dynoDriver DynoDriver
	formation  *heroku.Formation
	quantity   int
	release    *Release
	state      DynoState

	// docker dyno driver properties
	containers []*docker.Container
}

func (e *Executor) Args() []string {
	if e.formation != nil {
		return []string{e.formation.Command}
	} else {
		return e.argv
	}
}

func (e *Executor) Name() string {
	if e.formation != nil {
		return e.formation.Type
	} else {
		return "run"
	}
}

func (e *Executor) Start() {
again:
	s := e.state
	switch s {
	case NeverStarted:
		fallthrough
	case Stopped:
		log.Println("starting")
		err := e.dynoDriver.Start(e)
		if err != nil {
			log.Println(
				"process could not start with error:",
				err)
		}
		e.state = Started
		log.Println("started")
	case Started:
		log.Println("Attempting to stop...")
		err := e.dynoDriver.Stop(e)
		if err != nil {
			log.Println("process stopped with error:", err)
		}
		e.state = Stopped
		log.Println("...stopped")
		goto again
	default:
		log.Fatalln("BUG bad state:", s)
	}
}

func (e *Executor) Stop() {
	err := e.dynoDriver.Stop(e)
	if err != nil {
		log.Println("process stopped with error:", err)
	}
}

type DynoState int

const (
	NeverStarted DynoState = iota
	Started
	Stopped
)

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

type DynoDriver interface {
	Build(*Release) error
	Start(*Executor) error
	Stop(*Executor) error
}
