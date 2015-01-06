package main

import (
	"log"
	"time"
)

type DirPoller struct {
	Dd      DynoDriver
	Dir     string
	AppName string

	c             *conf
	lastReleaseID string
}

type ControlDir struct {
	Version   int
	Env       map[string]string
	Slug      string
	Processes []DirFormation
}

type DirFormation struct {
	FArgs     []string `json:"Args"`
	FQuantity int      `json:"Quantity"`
	FType     string   `json:"Type"`
}

func (f *DirFormation) Args() []string {
	return f.FArgs
}
func (f *DirFormation) Quantity() int {
	return f.FQuantity
}

func (f *DirFormation) Type() string {
	return f.FType
}

func newControlDir() interface{} {
	return &ControlDir{}
}

func (dp *DirPoller) Poll() <-chan *Processes {
	out := make(chan *Processes)
	dp.c = newConf(newControlDir, dp.Dir)
	go dp.pollSynchronous(out)
	return out
}

func (dp *DirPoller) pollSynchronous(out chan<- *Processes) {
	for {
		var cd *ControlDir
		var procs *Processes

		newInfo, err := dp.c.Poll()
		if err != nil {
			log.Println("Could not fetch new release information:",
				err)
			goto wait
		}

		if !newInfo {
			goto wait
		}

		cd = dp.c.Snapshot().(*ControlDir)
		procs = &Processes{
			r: &Release{
				appName: dp.AppName,
				config:  cd.Env,
				slugURL: cd.Slug,
				version: cd.Version,
			},
			forms: make([]Formation, len(cd.Processes)),
			dd:    dp.Dd,
		}

		for i := range cd.Processes {
			procs.forms[i] = &cd.Processes[i]
		}

		out <- procs
	wait:
		time.Sleep(10 * time.Second)
	}
}
