package hsup

import (
	"encoding/gob"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"code.google.com/p/go-uuid/uuid"

	"github.com/docker/libcontainer"
	"github.com/docker/libcontainer/cgroups"
	"github.com/docker/libcontainer/devices"
	"github.com/docker/libcontainer/mount"
	"github.com/docker/libcontainer/namespaces"
)

type LibContainerDynoDriver struct {
	workDir       string
	stacksDir     string
	containersDir string
}

func NewLibContainerDynoDriver(workDir string) (*LibContainerDynoDriver, error) {
	var (
		stacksDir     = filepath.Join(workDir, "stacks")
		containersDir = filepath.Join(workDir, "containers")
	)
	if err := os.MkdirAll(stacksDir, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(containersDir, 0755); err != nil {
		return nil, err
	}
	return &LibContainerDynoDriver{
		workDir:       workDir,
		stacksDir:     stacksDir,
		containersDir: containersDir,
	}, nil
}

func (dd *LibContainerDynoDriver) Build(release *Release) error {
	stacks, err := HerokuStacksFromManifest(dd.stacksDir)
	if err != nil {
		return err
	}
	for _, stack := range stacks {
		if strings.TrimSpace(stack.Name) != release.stack {
			continue
		}
		if err := stack.mount(); err != nil {
			return err
		}
	}
	return nil
}

func (dd *LibContainerDynoDriver) Start(ex *Executor) error {
	containerUUID := uuid.New()
	ex.containerUUID = containerUUID
	stackImagePath, err := CurrentStackImagePath(
		dd.stacksDir, ex.Release.stack,
	)
	if err != nil {
		return err
	}
	dataPath := filepath.Join(dd.containersDir, containerUUID)
	if err := os.MkdirAll(dataPath, 0755); err != nil {
		return err
	}
	var (
		appPath    = filepath.Join(dataPath, "app")
		tmpPath    = filepath.Join(dataPath, "tmp")
		varTmpPath = filepath.Join(dataPath, "var", "tmp")
		rootFSPath = filepath.Join(dataPath, "root")
	)
	// TODO: chown to the unprivileged user
	if err := os.MkdirAll(appPath, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(varTmpPath, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(rootFSPath, 0755); err != nil {
		return err
	}

	// bind mounts
	if err := syscall.Mount(
		stackImagePath, rootFSPath, "bind",
		syscall.MS_RDONLY|syscall.MS_BIND, "",
	); err != nil {
		return err
	}
	if err := syscall.Mount(
		tmpPath, filepath.Join(rootFSPath, "tmp"), "bind",
		syscall.MS_BIND, "",
	); err != nil {
		return err
	}

	// TODO: inject /tmp/slug.tgz if local

	where, err := filepath.Abs(linuxAmd64Path())
	if err != nil {
		return err
	}
	if err := copyFile(where, filepath.Join(tmpPath, "hsup"), 0755); err != nil {
		return err
	}

	// TODO tty
	console := ""

	ex.lcStatus = make(chan *ExitStatus)
	ex.waitStartup = make(chan struct{})
	ex.waitWait = make(chan struct{})

	cfgReader, cfgWriter, err := os.Pipe()
	initCtx := &containerInit{
		hsupBinaryPath: where,
		ex:             ex,
		configPipe:     cfgReader,
	}
	container := containerConfig(
		containerUUID, dataPath, rootFSPath, ex.Release.ConfigSlice(),
	)

	// send config to the init process inside the container
	go func() {
		defer cfgWriter.Close()
		encoder := gob.NewEncoder(cfgWriter)
		if err := encoder.Encode(container); err != nil {
			log.Fatal(err)
		}
	}()

	go func() {
		// TODO: stop swallowing errors
		code, err := namespaces.Exec(
			container, os.Stdin, os.Stdout, os.Stderr,
			console, dataPath, []string{},
			initCtx.createCommand, nil, initCtx.startCallback,
		)
		log.Println(code, err)
		ex.lcStatus <- &ExitStatus{Code: code, Err: err}
		close(ex.lcStatus)
	}()

	return nil
}

func (dd *LibContainerDynoDriver) Wait(ex *Executor) (s *ExitStatus) {
	s = <-ex.lcStatus
	close(ex.waitWait)
	go func() {
		ex.waiting <- struct{}{}
	}()

	return s
}

func (dd *LibContainerDynoDriver) Stop(ex *Executor) error {
	// TODO: just send a Stop() message to the container's init

	<-ex.waitStartup
	// Some caller already successfully got a return from "Wait",
	// which means the process exited: nothing to do.
	if _, ok := <-ex.waitWait; !ok {
		return nil
	}

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

type containerInit struct {
	hsupBinaryPath string
	ex             *Executor
	configPipe     *os.File
}

func (ctx *containerInit) createCommand(container *libcontainer.Config, console,
	dataPath, init string, controlPipe *os.File, args []string) *exec.Cmd {

	state := AppSerializable{
		Version: ctx.ex.Release.version,
		Env:     ctx.ex.Release.config,
		Slug:    ctx.ex.Release.slugURL,
		Stack:   ctx.ex.Release.stack,
		Processes: []FormationSerializable{
			{
				FArgs:     ctx.ex.Args,
				FQuantity: 1,
				FType:     ctx.ex.ProcessType,
			},
		},
	}
	cmd := exec.Command(ctx.hsupBinaryPath,
		"-d", "libcontainer-init", "-a", ctx.ex.Release.appName,
		"--oneshot", "--start-number="+ctx.ex.ProcessID,
		"start", ctx.ex.ProcessType,
	)
	cmd.Env = []string{
		"HSUP_SKIP_BUILD=TRUE",
		"HSUP_CONTROL_GOB=" + state.ToBase64Gob(),
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags = uintptr(
		namespaces.GetNamespaceFlags(container.Namespaces),
	)
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	cmd.ExtraFiles = []*os.File{controlPipe, ctx.configPipe}
	ctx.ex.cmd = cmd
	return cmd
}

func (ctx *containerInit) startCallback() {
	//TODO: log("Starting process web.1 with command `...`")
	close(ctx.ex.waitStartup)
	//child process is already running, it's safe to close the parent's read
	//side of the pipe
	ctx.configPipe.Close()
}

type LibContainerInitDriver struct{}

func (dd *LibContainerInitDriver) Build(*Release) error {
	// noop
	return nil
}

// Start acts as PID=1 inside a container spawned by libcontainer, doing the
// required setup and re-exec'ing as the abspath driver
// TODO: drop privileges (setuid)
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
	as := AppSerializable{
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
	}
	args := []string{
		"/tmp/hsup", "-d", "abspath", "-a", ex.Release.appName,
		"--oneshot", "--start-number=" + ex.ProcessID,
		"start", ex.ProcessType,
	}
	container.Env = []string{
		"HSUP_SKIP_BUILD=TRUE",
		"HSUP_CONTROL_GOB=" + as.ToBase64Gob(),
	}
	// TODO: clean up /tmp/hsup and /tmp/slug.tgz after abspath reads them
	return namespaces.Init(
		&container, container.RootFs, "",
		os.NewFile(3, "controlPipe"), args,
	)
}

func (dd *LibContainerInitDriver) Stop(*Executor) error {
	panic("this should never be called")
	return nil
}

func (dd *LibContainerInitDriver) Wait(*Executor) *ExitStatus {
	// this should be unreachable, but in case it is called, sleep forever:
	select {}
	return nil
}

func containerConfig(
	containerUUID, dataPath, rootFSPath string, env []string,
) *libcontainer.Config {
	return &libcontainer.Config{
		MountConfig: &libcontainer.MountConfig{
			PivotDir: "/tmp",
			Mounts: []*mount.Mount{
				{
					Type:        "bind",
					Destination: "/app",
					Writable:    true,
					Source: filepath.Join(
						dataPath,
						"app",
					),
				},
				{
					Type:        "bind",
					Destination: "/var/tmp",
					Writable:    true,
					Source: filepath.Join(
						dataPath,
						"var", "tmp",
					),
				},
				{
					Type:        "bind",
					Writable:    false,
					Destination: "/etc/resolv.conf",
					Source:      "/etc/resolv.conf",
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
		RootFs:   rootFSPath,
		Hostname: containerUUID,
		User:     "0:0",
		Env:      env,
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
		},
		Cgroups: &cgroups.Cgroup{
			Name: containerUUID,
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
}
