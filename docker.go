package main

import (
	"fmt"
	"github.com/fsouza/go-dockerclient"
)

type StackImage struct {
	stack   string
	repoTag string
	present bool
}

type Docker struct {
	c *docker.Client
}

func (d *Docker) Connect() (err error) {
	endpoint := "unix:///var/run/docker.sock"
	d.c, err = docker.NewClient(endpoint)
	return err
}

func (d *Docker) StackStat(stack string) (*StackImage, error) {
	si := StackImage{}
	switch stack {
	case "cedar-14":
		si.repoTag = "heroku/cedar:14"
	default:
		return nil, fmt.Errorf("unrecognized stack: %s", stack)
	}

	si.stack = stack

	imgs, err := d.c.ListImages(docker.ListImagesOptions{All: true})
	if err != nil {
		return nil, err
	}

	for _, img := range imgs {
		for _, tag := range img.RepoTags {
			if tag == si.repoTag {
				si.present = true
				return &si, nil
			}
		}
	}

	return &si, nil
}

func (d *Docker) Pull(si *StackImage) error {
	return nil
}
