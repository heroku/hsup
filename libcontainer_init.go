// +build linux

package hsup

import (
	"encoding/gob"
	"os"
	"runtime"
	"strings"

	"github.com/docker/libcontainer"
	"github.com/docker/libcontainer/namespaces"
)

// LibContainerInitDriver is intended to be used as init (PID=1) inside
// containers created by the libcontainer driver. It exists solely to be called
// by the libcontainer driver and should not be used directly.
type LibContainerInitDriver struct{}

func (dd *LibContainerInitDriver) Build(*Release) error {
	return nil // noop
}

// Start acts as PID=1 inside a container spawned by libcontainer, doing the
// required setup and re-exec'ing as the abspath driver
func (dd *LibContainerInitDriver) Start(ex *Executor) error {
	configPipe := os.NewFile(4, "configPipe")
	var container libcontainer.Config
	decoder := gob.NewDecoder(configPipe)
	if err := decoder.Decode(&container); err != nil {
		configPipe.Close()
		return err
	}
	configPipe.Close()

	dynoEnv := make(map[string]string, len(container.Env))
	for _, entry := range container.Env {
		pieces := strings.SplitN(entry, "=", 2)
		dynoEnv[pieces[0]] = pieces[1]
	}
	hs := Startup{
		App: AppSerializable{
			Version: ex.Release.version,
			Env:     dynoEnv,
			Slug:    ex.Release.slugURL,
			Stack:   ex.Release.stack,
			Processes: []FormationSerializable{
				{
					FArgs:     ex.Args,
					FQuantity: 1,
					FType:     ex.ProcessType,
				},
			},
		},
		OneShot:     true,
		SkipBuild:   false,
		StartNumber: ex.ProcessID,
		Action:      Start,
		Driver:      &AbsPathDynoDriver{},
		FormName:    ex.ProcessType,
		LogplexURL:  ex.logplexURLString(),
	}
	args := []string{"/usr/bin/setuidgid", "dyno", "/tmp/hsup"}
	container.Env = []string{"HSUP_CONTROL_GOB=" + hs.ToBase64Gob()}

	runtime.LockOSThread() // required by namespaces.Init

	// TODO: clean up /tmp/hsup and /tmp/slug.tgz after abspath reads them
	return namespaces.Init(
		&container, container.RootFs, "",
		os.NewFile(3, "controlPipe"), args,
	)
}

func (dd *LibContainerInitDriver) Stop(*Executor) error {
	// unreachable: this driver re-execs itself on Start()
	panic("this should never be called")
}

func (dd *LibContainerInitDriver) Wait(*Executor) *ExitStatus {
	// this should be unreachable, but in case it is called, sleep forever:
	select {}
}
