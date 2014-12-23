package main

import (
	"fmt"

	"github.com/cyberdelia/heroku-go/v3"
)

type Release struct {
	appName string
	config map[string]string
	slugUrl string
	version int
}

func (r *Release) Name() string {
	return fmt.Sprintf("%v-%v", r.appName, r.version)
}

type Executable interface {
	Args() []string
}

type Api3Executor struct {
	argv      []string
	formation *heroku.Formation
}

func (b *Api3Executor) Args() []string {
	if b.formation != nil {
		return []string{b.formation.Command}
	} else {
		return b.argv
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
		return NewSimpleDynoDriver(), nil
	case "docker":
		return NewDockerDynoDriver(), nil
	default:
		return nil, fmt.Errorf("could not locate driver. "+
			"specified by the user: %v", name)
	}
}

type DynoDriver interface {
	Build(*Release) error
	Start(*Release, Executable) error
	Stop() error
	State() DynoState
}
