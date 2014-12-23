package main

import (
	"fmt"

	"github.com/cyberdelia/heroku-go/v3"
)

type Release struct {
	appName string
	slugUrl string
	version int
}

func (r *Release) Name() string {
	return fmt.Sprintf("%v-%v", r.appName, r.version)
}

type Executable interface {
	Args() []string
	Config() map[string]string
	SlugUrl() string
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
