// +build linux

package hsup

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"code.google.com/p/go-uuid/uuid"

	"github.com/docker/libcontainer"
	"github.com/docker/libcontainer/cgroups"
	"github.com/docker/libcontainer/devices"
	"github.com/docker/libcontainer/mount"
	"github.com/docker/libcontainer/namespaces"
	"github.com/docker/libcontainer/network"
)

type LibContainerDynoDriver struct {
	workDir       string
	stacksDir     string
	containersDir string
	allocator     *Allocator
}

func init() {
	network.AddStrategy("routed", &Routed{})
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
	allocator, err := NewAllocator(workDir)
	if err != nil {
		return nil, err
	}
	return &LibContainerDynoDriver{
		workDir:       workDir,
		stacksDir:     stacksDir,
		containersDir: containersDir,
		allocator:     allocator,
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

func (dd *LibContainerDynoDriver) networkFor(uid, port int) (*libcontainer.Network, portMapper, error) {
	// allow backwards compatibility while users stop relying on static IPs
	// previously being assigned to containers. This will be removed soon.
	var (
		staticIP = os.Getenv("LIBCONTAINER_DYNO_IP")
		staticGW = os.Getenv("LIBCONTAINER_GW_IP")
		bridge   = os.Getenv("LIBCONTAINER_BRIDGE")
	)
	if staticIP != "" && staticGW != "" && bridge != "" {
		// fall back to libcontainer's own network strategy and let the
		// container be attached to a bridge
		return &libcontainer.Network{
			Address:    staticIP,
			VethPrefix: "veth",
			Gateway:    staticGW,
			Bridge:     bridge,
			Mtu:        1500,
			Type:       "veth",
		}, &noopPortMapper{}, nil
	}

	// allocate a dynamic subnet when no static configuration is provided
	sn, err := dd.allocator.privateNetForUID(uid)
	if err != nil {
		return nil, nil, err
	}
	subnet, err := newSmallSubnet(sn)
	if err != nil {
		return nil, nil, err
	}
	return &libcontainer.Network{
			Address:    subnet.Host().String(),
			VethPrefix: fmt.Sprintf("veth%d", uid),
			Gateway:    subnet.Gateway().IP.String(),
			Mtu:        1500,
			Type:       "routed",
		}, &iptablesPortMapper{
			chainId: uid,
			port:    port,
			subnet:  subnet,
		}, nil
}

func (dd *LibContainerDynoDriver) Start(ex *Executor) error {
	containerUUID := uuid.New()
	port, err := dd.allocator.ReservePort()
	if err != nil {
		return err
	}
	uid, err := dd.allocator.ReserveUID()
	if err != nil {
		return err
	}
	network, portMapper, err := dd.networkFor(uid, port)
	if err != nil {
		return err
	}
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
	writablePaths := []string{
		filepath.Join(dataPath, "app"),
		filepath.Join(dataPath, "tmp"),
		filepath.Join(dataPath, "var", "tmp"),
	}
	for _, path := range writablePaths {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
		if err := os.Chown(path, uid, uid); err != nil {
			return err
		}
	}
	rootFSPath := filepath.Join(dataPath, "root")
	if err := os.MkdirAll(rootFSPath, 0755); err != nil {
		return err
	}

	// stack image is the rootFS
	if err := syscall.Mount(
		stackImagePath, rootFSPath, "bind",
		syscall.MS_RDONLY|syscall.MS_BIND, "",
	); err != nil {
		return err
	}

	if err := createPasswdWithDynoUser(
		stackImagePath, dataPath, uid,
	); err != nil {
		return err
	}

	if ex.Release.Where() == Local {
		// move into the container
		if err := copyFile(
			ex.Release.slugURL,
			filepath.Join(dataPath, "tmp", "slug.tgz"),
			0644,
		); err != nil {
			return err
		}
		ex.Release.slugURL = "/tmp/slug.tgz"
	}

	outsideContainer, err := filepath.Abs(linuxAmd64Path())
	if err != nil {
		return err
	}
	insideContainer := filepath.Join(dataPath, "tmp", "hsup")
	if err := copyFile(outsideContainer, insideContainer, 0755); err != nil {
		return err
	}

	// TODO tty
	console := ""

	ex.initExitStatus = make(chan *ExitStatus)

	cfgReader, cfgWriter, err := os.Pipe()
	initCtx := &containerInit{
		hsupBinaryPath: outsideContainer,
		portMapper:     portMapper,
		ex:             ex,
		configPipe:     cfgReader,
	}

	container := containerConfig(
		containerUUID, uid, dataPath, network, ex.Release.ConfigSlice(),
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
		if err != nil {
			log.Printf("namespaces.Exec fails: %q", err)
		}

		// GC
		// TODO: gc after sending back the exit status
		// doing so right now terminates the program too early,
		// before everything is removed
		if err := syscall.Unmount(rootFSPath, 0); err != nil {
			log.Printf("unmount error: %#+v", err)
		}
		for _, path := range writablePaths {
			if err := os.RemoveAll(path); err != nil {
				log.Printf("remove all error: %#+v", err)
			}
		}
		if err := os.RemoveAll(dataPath); err != nil {
			log.Printf("remove all error: %#+v", err)
		}
		if err := portMapper.Destroy(); err != nil {
			log.Printf("error cleaning up iptables rules: %q", err)
		}

		// it's probably safe to ignore errors here. Worst case
		// scenario, this uid/port won't be be reused.
		dd.allocator.FreeUID(uid)
		dd.allocator.FreePort(port)

		ex.initExitStatus <- &ExitStatus{Code: code, Err: err}
		close(ex.initExitStatus)
	}()

	return nil
}

func (dd *LibContainerDynoDriver) Wait(ex *Executor) (s *ExitStatus) {
	return <-ex.initExitStatus
}

func (dd *LibContainerDynoDriver) Stop(ex *Executor) error {
	if ex.cmd.ProcessState != nil {
		return nil // already exited
	}

	// TODO: fix a race conditition when Stop() is called before the
	// libcontainer driver re-execs itself

	// tell the abspath-driver to stop
	return ex.cmd.Process.Signal(syscall.SIGTERM)
}

func createPasswdWithDynoUser(stackImagePath, dataPath string, uid int) error {
	var contents bytes.Buffer
	original, err := os.Open(filepath.Join(stackImagePath, "etc", "passwd"))
	if err != nil {
		return err
	}
	defer original.Close()

	if _, err := contents.ReadFrom(original); err != nil {
		return err
	}
	dynoUser := fmt.Sprintf("\ndyno:x:%d:%d::/app:/bin/bash\n", uid, uid)
	if _, err := contents.WriteString(dynoUser); err != nil {
		return err
	}

	dst, err := os.Create(filepath.Join(dataPath, "passwd"))
	if err != nil {
		return err
	}
	defer dst.Close()
	if err := dst.Chmod(0644); err != nil {
		return err
	}

	_, err = contents.WriteTo(dst)
	return err
}

type containerInit struct {
	hsupBinaryPath string
	portMapper     portMapper
	ex             *Executor
	configPipe     *os.File
}

func (ctx *containerInit) createCommand(container *libcontainer.Config, console,
	dataPath, init string, controlPipe *os.File, args []string) *exec.Cmd {

	hs := Startup{
		App: AppSerializable{
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
			LogplexURL: ctx.ex.logplexURLString(),
		},
		OneShot:     true,
		StartNumber: ctx.ex.ProcessID,
		Action:      Start,
		Driver:      &LibContainerInitDriver{},
		FormName:    ctx.ex.ProcessType,
	}
	cmd := exec.Command(ctx.hsupBinaryPath)
	cmd.Env = []string{"HSUP_CONTROL_GOB=" + hs.ToBase64Gob()}
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
	if err := ctx.portMapper.Create(); err != nil {
		log.Fatal(err)
	}

	//child process is already running, it's safe to close the parent's read
	//side of the pipe
	ctx.configPipe.Close()
}

func containerConfig(
	containerUUID string,
	uid int,
	dataPath string,
	network *libcontainer.Network,
	env []string,
) *libcontainer.Config {
	return &libcontainer.Config{
		MountConfig: &libcontainer.MountConfig{
			Mounts: []*mount.Mount{
				{
					Type:        "bind",
					Destination: "/app",
					Writable:    true,
					Source:      filepath.Join(dataPath, "app"),
				},
				{
					Type:        "bind",
					Destination: "/tmp",
					Writable:    true,
					Source:      filepath.Join(dataPath, "tmp"),
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
					Destination: "/etc/passwd",
					Writable:    false,
					Source: filepath.Join(
						dataPath, "passwd",
					),
				},
				{
					Type:        "bind",
					Writable:    false,
					Destination: "/etc/resolv.conf",
					Source:      "/etc/resolv.conf",
				},
			},
			MountLabel:  containerUUID,
			DeviceNodes: devices.DefaultSimpleDevices,
			PivotDir:    "/tmp",
		},
		RootFs:   filepath.Join(dataPath, "root"),
		Hostname: containerUUID,
		User:     fmt.Sprintf("%d:%d", uid, uid),
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
			network,
		},
		Cgroups: &cgroups.Cgroup{
			Name:           containerUUID,
			AllowedDevices: devices.DefaultAllowedDevices,
		},
	}
}
