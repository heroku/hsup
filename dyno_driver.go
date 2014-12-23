package main

import (
	"fmt"

	"github.com/cyberdelia/heroku-go/v3"
)

type Executable interface {
	Args() []string
	Config() map[string]string
	SlugUrl() string
	App() string
	Version() int
}

type Api3Executor struct {
	app       string
	argv      []string
	config    map[string]string
	formation *heroku.Formation
	release   *heroku.Release
	slug      *heroku.Slug
}

func (b *Api3Executor) Args() []string {
	var cmd []string
	if b.formation != nil {
		cmd = []string{b.formation.Command}
	} else {
		cmd = b.argv
	}
	return cmd
}

func (b *Api3Executor) Config() map[string]string {
	return b.config
}

func (b *Api3Executor) SlugUrl() string {
	return b.slug.Blob.URL
}

func (b *Api3Executor) App() string {
	return b.app
}

func (b *Api3Executor) Version() int {
	return b.release.Version
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
	Start(Executable) error
	Stop() error
	State() DynoState
}
