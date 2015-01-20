package main

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"log"
	"strings"
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

func (dp *DirPoller) Notify() <-chan *Processes {
	out := make(chan *Processes)
	dp.c = newConf(newControlDir, dp.Dir)
	go dp.pollSynchronous(out)
	return out
}

func procsFromControlDir(cd *ControlDir, app string, oneShot bool,
	dd DynoDriver) *Processes {
	procs := &Processes{
		r: &Release{
			appName: app,
			config:  cd.Env,
			slugURL: cd.Slug,
			version: cd.Version,
		},
		forms:   make([]Formation, len(cd.Processes)),
		dd:      dd,
		OneShot: oneShot,
	}

	for i := range cd.Processes {
		procs.forms[i] = &cd.Processes[i]
	}

	return procs
}

func (dp *DirPoller) pollSynchronous(out chan<- *Processes) {
	for {
		var cd *ControlDir

		newInfo, err := dp.c.Notify()
		if err != nil {
			log.Println("Could not fetch new release information:",
				err)
			goto wait
		}

		if !newInfo {
			goto wait
		}

		cd = dp.c.Snapshot().(*ControlDir)
		out <- procsFromControlDir(cd, dp.AppName, dp.OneShot, dp.Dd)
	wait:
		time.Sleep(10 * time.Second)
	}
}

type GobNotifier struct {
	Dd      DynoDriver
	AppName string
	OneShot bool

	Payload string
}

func (cd *ControlDir) textGob() string {
	buf := bytes.Buffer{}
	b64enc := base64.NewEncoder(base64.StdEncoding, &buf)
	enc := gob.NewEncoder(b64enc)
	err := enc.Encode(cd)
	b64enc.Close()
	if err != nil {
		panic("could not encode gob:" + err.Error())
	}

	return buf.String()
}

func (gn *GobNotifier) Notify() <-chan *Processes {
	out := make(chan *Processes)
	d := gob.NewDecoder(base64.NewDecoder(base64.StdEncoding,
		strings.NewReader(gn.Payload)))
	cd := new(ControlDir)
	if err := d.Decode(cd); err != nil {
		panic("could not decode gob:" + err.Error())
	}

	procs := procsFromControlDir(cd, gn.AppName, gn.OneShot, gn.Dd)
	go func() {
		out <- procs
	}()

	return out
}
