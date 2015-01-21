package hsup

import (
	"bitbucket.org/kardianos/osext"
	"errors"
	"log"
	"runtime"
)

var ErrNoReleases = errors.New("No releases found")

type Notifier interface {
	Notify() <-chan *Processes
}

type Processes struct {
	Rel   *Release
	Forms []Formation

	Dd        DynoDriver
	OneShot   bool
	Executors []*Executor
}

type Formation interface {
	Args() []string
	Quantity() int
	Type() string
}

func linuxAmd64Path() string {
	exe, err := osext.Executable()
	if err != nil {
		log.Fatalf("could not locate own executable:", err)
	}

	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return exe
	}

	return exe + "-linux-amd64"
}
