package hsup

import (
	"errors"
	"os"
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
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return os.Args[0]
	}

	return os.Args[0] + "-linux-amd64"
}
