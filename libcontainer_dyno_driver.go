package main

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/docker/libcontainer"
	"github.com/docker/libcontainer/cgroups"
	"github.com/docker/libcontainer/devices"
	"github.com/docker/libcontainer/mount"
	"github.com/docker/libcontainer/namespaces"
)

type LibContainerDynoDriver struct {
	// LibContainer specific state.
	NewRoot, Hostname, User string

	// Filled to construct an abspath-driver invocation of hsup.
	AppName     string
	Concurrency int
	Args, Env   []string
}

type lcCallbacks struct {
	ex *Executor
	dd *LibContainerDynoDriver
}

type initReturnArgs struct {
	Container     *libcontainer.Config
	UncleanRootfs string
	ConsolePath   string
}

func (ira *initReturnArgs) Env() string {
	buf := bytes.Buffer{}
	b64enc := base64.NewEncoder(base64.StdEncoding, &buf)
	enc := gob.NewEncoder(b64enc)
	err := enc.Encode(&ira)
	b64enc.Close()
	if err != nil {
		panic("could not encode initReturnArgs gob")
	}

	return "HSUP_INITRETURN_DATA=" + buf.String()
}

func mustInit(irData string) (err error) {
	d := gob.NewDecoder(base64.NewDecoder(base64.StdEncoding,
		strings.NewReader(irData)))
	ira := new(initReturnArgs)
	if err = d.Decode(ira); err != nil {
		panic("could not decode initReturnArgs")
	}
	log.Printf("init cmd: %#+v", os.Args[1:])

	return namespaces.Init(ira.Container, ira.UncleanRootfs,
		ira.ConsolePath, os.NewFile(3, "pipe"), os.Args[1:])
}

func (cb *lcCallbacks) CreateCommand(container *libcontainer.Config, console,
	dataPath, init string, pipe *os.File, args []string) *exec.Cmd {

	ex := cb.ex
	ex.cmd = exec.Command(os.Args[0], ex.args...)

	ira := initReturnArgs{Container: container,
		UncleanRootfs: cb.dd.NewRoot, ConsolePath: ""}

	// Set up abspath driver environment.
	ex.cmd.Env = append([]string{ira.Env()}, cb.dd.Env...)

	if ex.cmd.SysProcAttr == nil {
		ex.cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	ex.cmd.SysProcAttr.Cloneflags = uintptr(
		namespaces.GetNamespaceFlags(
			container.Namespaces))

	ex.cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	ex.cmd.ExtraFiles = []*os.File{pipe}

	return ex.cmd
}

func (cb *lcCallbacks) StartCallback() {
	log.Println("closing from StartCallback")
	close(cb.ex.waitStartup)
}

func (dd *LibContainerDynoDriver) envFill() {
	appendPresent := func(name string) {
		val := os.Getenv(name)
		if val != "" {
			dd.Env = append(dd.Env, name+"="+val)
		}
	}
	appendPresent("HEROKU_ACCESS_TOKEN")
	appendPresent("CONTROL_DIR")
}

func (dd *LibContainerDynoDriver) Build(release *Release) error {
	return nil
}

func (dd *LibContainerDynoDriver) Start(ex *Executor) error {
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	ex.lcStatus = make(chan *ExitStatus)
	ex.waitStartup = make(chan struct{})
	ex.waitWait = make(chan struct{})
	cb := lcCallbacks{ex: ex, dd: dd}

	go func() {
		code, err := namespaces.Exec(dd.lcconf(ex),
			os.Stdin, os.Stdout, os.Stderr, "", pwd, []string{},
			cb.CreateCommand, cb.StartCallback)
		log.Println(code, err)
		ex.lcStatus <- &ExitStatus{code: code, err: err}
		close(ex.lcStatus)
	}()

	return nil
}

func (dd *LibContainerDynoDriver) Wait(ex *Executor) (s *ExitStatus) {
	s = <-ex.lcStatus
	close(ex.waitWait)
	return s
}

func (dd *LibContainerDynoDriver) Stop(ex *Executor) error {
	<-ex.waitStartup
	// Some caller already successfully got a return from "Wait",
	// which means the process exited: nothing to do.
	if _, ok := <-ex.waitWait; !ok {
		return nil
	}

	<-ex.lcStatus
	p := ex.cmd.Process

	// Begin graceful shutdown via SIGTERM.
	p.Signal(syscall.SIGTERM)

	for {
		select {
		case <-time.After(10 * time.Second):
			log.Println("sigkill", p)
			p.Signal(syscall.SIGKILL)
		case <-ex.waiting:
			log.Println("waited", p)
			return nil
		}
		log.Println("spin", p)
		time.Sleep(1)
	}
}

func (dd *LibContainerDynoDriver) lcconf(ex *Executor) *libcontainer.Config {
	lc := &libcontainer.Config{
		MountConfig: &libcontainer.MountConfig{
			Mounts: []*mount.Mount{
				{
					Type:        "tmpfs",
					Destination: "/tmp",
				},
				{
					Type:        "bind",
					Source:      "/etc/resolv.conf",
					Destination: "/etc/resolv.conf",
				},
			},
			DeviceNodes: []*devices.Device{
				{
					Type:              99,
					Path:              "/dev/null",
					MajorNumber:       1,
					MinorNumber:       3,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/zero",
					MajorNumber:       1,
					MinorNumber:       5,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/full",
					MajorNumber:       1,
					MinorNumber:       7,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/tty",
					MajorNumber:       5,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/urandom",
					MajorNumber:       1,
					MinorNumber:       9,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/random",
					MajorNumber:       1,
					MinorNumber:       8,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
			},
		},
		RootFs:   dd.NewRoot,
		Hostname: dd.Hostname,
		User:     "0:0",
		Env:      ex.release.ConfigSlice(),
		Namespaces: []libcontainer.Namespace{
			{Type: "NEWIPC"},
			{Type: "NEWNET"},
			{Type: "NEWNS"},
			{Type: "NEWPID"},
			{Type: "NEWUTS"},
		},
		Capabilities: []string{
			"CHOWN",
			"DAC_OVERRIDE",
			"FOWNER",
			"MKNOD",
			"NET_RAW",
			"SETGID",
			"SETUID",
			"SETFCAP",
			"SETPCAP",
			"NET_BIND_SERVICE",
			"SYS_CHROOT",
			"KILL",
		},
		Networks: []*libcontainer.Network{
			{
				Address: "127.0.0.1/0",
				Gateway: "localhost",
				Mtu:     1500,
				Type:    "loopback",
			},
			{
				Address:    "172.17.0.101/16",
				Bridge:     "docker0",
				Gateway:    "172.17.42.1",
				Mtu:        1500,
				Type:       "veth",
				VethPrefix: "veth",
			},
		},
		Cgroups: &cgroups.Cgroup{
			Name: dd.Hostname,
			AllowedDevices: []*devices.Device{
				{
					Type:              99,
					MajorNumber:       -1,
					MinorNumber:       -1,
					CgroupPermissions: "m",
				},
				{
					Type:              98,
					MajorNumber:       -1,
					MinorNumber:       -1,
					CgroupPermissions: "m",
				},
				{
					Type:              99,
					Path:              "/dev/console",
					MajorNumber:       5,
					MinorNumber:       1,
					CgroupPermissions: "rwm",
				},
				{
					Type:              99,
					Path:              "/dev/tty0",
					MajorNumber:       4,
					CgroupPermissions: "rwm",
				},
				{
					Type:              99,
					Path:              "/dev/tty1",
					MajorNumber:       4,
					MinorNumber:       1,
					CgroupPermissions: "rwm",
				},
				{
					Type:              99,
					MajorNumber:       136,
					MinorNumber:       -1,
					CgroupPermissions: "rwm",
				},
				{
					Type:              99,
					MajorNumber:       5,
					MinorNumber:       2,
					CgroupPermissions: "rwm",
				},
				{
					Type:              99,
					MajorNumber:       10,
					MinorNumber:       200,
					CgroupPermissions: "rwm",
				},
				{
					Type:              99,
					Path:              "/dev/null",
					MajorNumber:       1,
					MinorNumber:       3,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/zero",
					MajorNumber:       1,
					MinorNumber:       5,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/full",
					MajorNumber:       1,
					MinorNumber:       7,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/tty",
					MajorNumber:       5,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/urandom",
					MajorNumber:       1,
					MinorNumber:       9,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
				{
					Type:              99,
					Path:              "/dev/random",
					MajorNumber:       1,
					MinorNumber:       8,
					CgroupPermissions: "rwm",
					FileMode:          438,
				},
			},
		},
	}

	return lc
}
