package hsup

import (
	"fmt"
)

type DynoDriver interface {
	Build(*Release) error
	Start(*Executor) error
	Stop(*Executor) error
	Wait(*Executor) *ExitStatus
}

type ExitStatus struct {
	Code int
	Err  error
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
		return &AbsPathDynoDriver{}, nil
	default:
		return nil, fmt.Errorf("could not locate driver. "+
			"specified by the user: %v", name)
	}
}
