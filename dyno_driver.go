package main

import (
	"fmt"

	"github.com/cyberdelia/heroku-go/v3"
	"github.com/fsouza/go-dockerclient"
)

type Release struct {
	appName string
	config map[string]string
	slugUrl string
	version int

	// docker dyno driver properties
	imageName string
}

func (r *Release) Name() string {
	return fmt.Sprintf("%v-%v", r.appName, r.version)
}

type Executor struct {
	argv      []string
	formation *heroku.Formation

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
	Start(*Release, *Executor) error
	Stop(*Executor) error
	State() DynoState
}
