package main

import (
	"fmt"
	"github.com/cyberdelia/heroku-go/v3"
)

type Bundle struct {
	config  map[string]string
	release *heroku.Release
	argv    []string
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
	Start(*Bundle) error
	Stop() error
	State() DynoState
}
