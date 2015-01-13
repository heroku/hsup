package main

import (
	"fmt"
	"os"
	"strconv"
)

type DynoDriver interface {
	Build(*Release) error
	Start(*Executor) error
	Stop(*Executor) error
	Wait(*Executor) *ExitStatus
}

type ExitStatus struct {
	code int
	err  error
}

type Release struct {
	appName string
	config  map[string]string
	slugURL string
	version int

	// docker dyno driver properties
	imageName string
}

func (r *Release) Name() string {
	return fmt.Sprintf("%v-%v", r.appName, r.version)
}

func FindDynoDriver(name string) (DynoDriver, error) {
	switch name {
	case "simple":
		return &SimpleDynoDriver{}, nil
	case "docker":
		return &DockerDynoDriver{}, nil
	case "abspath":
		utext := os.Getenv("HSUP_ABSPATH_UID")
		uid, err := strconv.Atoi(utext)
		if err != nil {
			return nil, fmt.Errorf("could not parse "+
				"HSUP_ABSPATH_UID as integer, was: %q", utext)
		}

		base := os.Getenv("HSUP_ABSPATH_BASE")
		if base == "" {
			return nil, fmt.Errorf("HSUP_ABSPATH_BASE empty")
		}
		return &AbsPathDynoDriver{
			Base: base,
			UID:  uint32(uid),
			GID:  uint32(uid),
		}, nil
	default:
		return nil, fmt.Errorf("could not locate driver. "+
			"specified by the user: %v", name)
	}
}
