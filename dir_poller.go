package hsup

import (
	"log"
	"time"
)

type DirPoller struct {
	Dd      DynoDriver
	Dir     string
	AppName string
	OneShot bool

	c             *conf
	lastReleaseID string
}

func newControlDir() interface{} {
	return &AppSerializable{}
}

func (dp *DirPoller) Notify() <-chan *Processes {
	out := make(chan *Processes)
	dp.c = newConf(newControlDir, dp.Dir)
	go dp.pollSynchronous(out)
	return out
}

func (dp *DirPoller) pollSynchronous(out chan<- *Processes) {
	for {
		var as *AppSerializable

		newInfo, err := dp.c.Notify()
		if err != nil {
			log.Println("Could not fetch new release information:",
				err)
			goto wait
		}

		if !newInfo {
			goto wait
		}

		as = dp.c.Snapshot().(*AppSerializable)
		out <- as.Procs(dp.AppName, dp.Dd, dp.OneShot)
	wait:
		time.Sleep(10 * time.Second)
	}
}
