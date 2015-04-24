package hsup

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var ErrIPNotFound = errors.New("ip not found")

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
	stack   string
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

func (r *Release) ConfigSlice() []string {
	var c []string
	for k, v := range r.config {
		c = append(c, k+"="+v)
	}
	return c
}

func DefaultIPInfo(ex *Executor) (IPInfo, error) {
	port, err := strconv.Atoi(ex.Release.config["PORT"])
	if err != nil {
		return nil, err
	}

	ip, err := lookupLocalIP()
	if err != nil {
		return nil, err
	}

	return func() (string, int) {
		return ip, port
	}, nil
}

func lookupLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		ip, ok := addr.(*net.IPNet)
		if !ok && ip.IP.IsLoopback() {
			continue
		}

		if v4 := ip.IP.To4(); v4 != nil {
			return v4.String(), nil
		}
	}

	return "", ErrIPNotFound
}
