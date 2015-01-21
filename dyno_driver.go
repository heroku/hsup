package hsup

import (
	"fmt"
	"strings"
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

type SlugWhere int

const (
	Local SlugWhere = iota
	HTTP
)

func (r *Release) Where() SlugWhere {
	switch {
	case strings.HasPrefix(r.slugURL, "http://"):
		fallthrough
	case strings.HasPrefix(r.slugURL, "https://"):
		return HTTP
	case strings.HasPrefix(r.slugURL, "file://"):
		fallthrough
	default:
		return Local
	}
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
