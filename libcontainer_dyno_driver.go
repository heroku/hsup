// +build linux

package hsup

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"code.google.com/p/go-uuid/uuid"

	"github.com/opencontainers/runc/libcontainer"
	"github.com/opencontainers/runc/libcontainer/configs"
)

var (
	dynoPrivateSubnet net.IPNet
	// default to max 8K dynos
	dynoMinUID int = 3000
	dynoMaxUID int = 11000
)

type LibContainerDynoDriver struct {
	workDir       string
	stacksDir     string
	containersDir string
	allocator     *Allocator
}

// init reads custom configuration from ENV vars:
// - LIBCONTAINER_DYNO_SUBNET
// - LIBCONTAINER_DYNO_UID_MIN
// - LIBCONTAINER_DYNO_UID_MAX
func init() {
	// hack to act as PID=1 inside containers
	if len(os.Args) > 1 && os.Args[1] == "libcontainer-init" {
		runtime.GOMAXPROCS(1)
		runtime.LockOSThread()
		factory, _ := libcontainer.New("")
		if err := factory.StartInitialization(); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		panic("this should never be executed")
	}

	dynoPrivateSubnet = DefaultPrivateSubnet
	if custom := strings.TrimSpace(
		os.Getenv("LIBCONTAINER_DYNO_SUBNET"),
	); len(custom) > 0 {
		baseIP, subnet, err := net.ParseCIDR(custom)
		if err != nil {
			panic(err)
		}
		dynoPrivateSubnet = net.IPNet{
			IP:   baseIP.To4(),
			Mask: subnet.Mask,
		}
	}

	//TODO: custom libnetwork driver
	//network.AddStrategy("routed", &Routed{privateSubnet: dynoPrivateSubnet})

	if minUID := strings.TrimSpace(
		os.Getenv("LIBCONTAINER_DYNO_UID_MIN"),
	); len(minUID) > 0 {
		min, err := strconv.Atoi(minUID)
		if err != nil {
			panic(err)
		}
		dynoMinUID = min
	}
	if maxUID := strings.TrimSpace(
		os.Getenv("LIBCONTAINER_DYNO_UID_MAX"),
	); len(maxUID) > 0 {
		max, err := strconv.Atoi(maxUID)
		if err != nil {
			panic(err)
		}
		dynoMaxUID = max
	}
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
	allocator, err := NewAllocator(
		workDir, dynoPrivateSubnet,
		dynoMinUID, dynoMaxUID,
	)
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

func (dd *LibContainerDynoDriver) networkFor(uid int) (*configs.Network, error) {
	sn, err := dd.allocator.privateNetForUID(uid)
	if err != nil {
		return nil, err
	}
	subnet, err := newSmallSubnet(sn)
	if err != nil {
		return nil, err
	}
	return &configs.Network{
		Address: subnet.Host().String(),
		Name:    fmt.Sprintf("veth%d", uid),
		Gateway: subnet.Gateway().IP.String(),
		Mtu:     1500,
		Type:    "routed",
	}, nil
}

func (dd *LibContainerDynoDriver) Start(ex *Executor) error {
	ex.initExitStatus = make(chan *ExitStatus)

	containerUUID := uuid.New()
	uid, err := dd.allocator.ReserveUID()
	if err != nil {
		return err
	}

	// Network
	network, err := dd.networkFor(uid)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(ex.Release.config["PORT"])
	if err != nil {
		return err
	}
	ex.IPInfo = func() (string, int) {
		ip := strings.Split(network.Address, "/")
		return ip[0], port
	}

	// Root FS
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
		filepath.Join(dataPath, "dev"),
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

	// Init (PID=1) hsup process with the abspath driver
	hsupConfig := Startup{
		App: AppSerializable{
			Version: ex.Release.version,
			Env:     ex.Release.config,
			Slug:    ex.Release.slugURL,
			Stack:   ex.Release.stack,
			Processes: []FormationSerializable{
				{
					FArgs:     ex.Args,
					FQuantity: 1,
					FType:     ex.ProcessType,
				},
			},
			LogplexURL: ex.logplexURLString(),
		},
		OneShot:     true,
		SkipBuild:   false,
		StartNumber: ex.ProcessID,
		Action:      Start,
		Driver:      &AbsPathDynoDriver{},
		FormName:    ex.ProcessType,
	}
	hsupInit := &libcontainer.Process{
		Args:   []string{"/tmp/hsup"},
		Env:    []string{"HSUP_CONTROL_GOB=" + hsupConfig.ToBase64Gob()},
		Cwd:    "/app",
		User:   fmt.Sprintf("%d:%d", uid, uid),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	ex.initProcess = hsupInit

	var container libcontainer.Container
	// GC
	defer func() {
		go func() {
			// TODO: stop swallowing errors
			code, err := hsupInit.Wait()
			if err != nil {
				log.Printf("process.Wait fails: %q", err)
			}

			// TODO: gc after sending back the exit status
			// doing so right now terminates the program too early,
			// before everything is removed
			if container != nil {
				if err := container.Destroy(); err != nil {
					log.Printf(
						"container.Destroy error: %#+v",
						err,
					)
				}
			}
			if err := syscall.Unmount(rootFSPath, 0); err != nil {
				log.Printf("unmount error: %#+v", err)
			}
			for _, path := range writablePaths {
				if err := os.RemoveAll(path); err != nil {
					log.Printf("remove all error: %#+v", err)
				}
			}
			if err := os.RemoveAll(dataPath); err != nil {
				log.Printf("datapath remove all error: %#+v", err)
			}

			// it's probably safe to ignore errors here. Worst case
			// scenario, this uid won't be be reused.
			dd.allocator.FreeUID(uid)

			// TODO: handle exit status when signaled
			ex.initExitStatus <- &ExitStatus{
				Code: code.Sys().(syscall.WaitStatus).ExitStatus(),
				Err:  err,
			}
			close(ex.initExitStatus)
		}()
	}()

	// Container Exec
	factory, err := libcontainer.New(
		filepath.Join(dd.containersDir, "libcontainer"),
		libcontainer.InitArgs(os.Args[0], "libcontainer-init"),
	)
	if err != nil {
		return err
	}
	container, err = factory.Create(containerUUID, containerConfig(
		containerUUID, dataPath,
	))
	if err != nil {
		return err
	}
	return container.Start(hsupInit)
}

func (dd *LibContainerDynoDriver) Wait(ex *Executor) (s *ExitStatus) {
	return <-ex.initExitStatus
}

func (dd *LibContainerDynoDriver) Stop(ex *Executor) error {
	// tell the abspath-driver to stop
	return ex.initProcess.Signal(syscall.SIGTERM)
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

const defaultMountFlags = syscall.MS_NOEXEC | syscall.MS_NOSUID | syscall.MS_NODEV

func containerConfig(containerUUID string, dataPath string) *configs.Config {
	return &configs.Config{
		Mounts: []*configs.Mount{
			{
				Source:      "proc",
				Destination: "/proc",
				Device:      "proc",
				Flags:       defaultMountFlags,
			},
			{
				Source:      "tmpfs",
				Destination: "/dev",
				Device:      "tmpfs",
				Flags:       syscall.MS_NOSUID | syscall.MS_STRICTATIME,
				Data:        "mode=755",
			},
			{
				Source:      "devpts",
				Destination: "/dev/pts",
				Device:      "devpts",
				Flags:       syscall.MS_NOSUID | syscall.MS_NOEXEC,
				Data:        "newinstance,ptmxmode=0666,mode=0620,gid=5",
			},
			{
				Device:      "tmpfs",
				Source:      "shm",
				Destination: "/dev/shm",
				Data:        "mode=1777,size=65536k",
				Flags:       defaultMountFlags,
			},
			{
				Source:      "mqueue",
				Destination: "/dev/mqueue",
				Device:      "mqueue",
				Flags:       defaultMountFlags,
			},
			{
				Source:      "sysfs",
				Destination: "/sys",
				Device:      "sysfs",
				Flags:       defaultMountFlags | syscall.MS_RDONLY,
			},
			{
				Device:      "bind",
				Flags:       syscall.MS_NOSUID | syscall.MS_BIND,
				Destination: "/app",
				Source:      filepath.Join(dataPath, "app"),
			},
			{
				Device:      "bind",
				Flags:       syscall.MS_NOSUID | syscall.MS_NOEXEC | syscall.MS_BIND,
				Destination: "/dev",
				Source:      filepath.Join(dataPath, "dev"),
			},
			{
				Device:      "bind",
				Flags:       syscall.MS_NOSUID | syscall.MS_BIND,
				Destination: "/tmp",
				Source:      filepath.Join(dataPath, "tmp"),
			},
			{
				Device:      "bind",
				Flags:       syscall.MS_NOSUID | syscall.MS_BIND,
				Destination: "/var/tmp",
				Source: filepath.Join(
					dataPath,
					"var", "tmp",
				),
			},
			{
				Device:      "bind",
				Flags:       syscall.MS_NOSUID | syscall.MS_BIND | syscall.MS_RDONLY,
				Destination: "/etc/passwd",
				Source: filepath.Join(
					dataPath, "passwd",
				),
			},
			{
				Device:      "bind",
				Flags:       syscall.MS_NOSUID | syscall.MS_BIND | syscall.MS_RDONLY,
				Destination: "/etc/resolv.conf",
				Source:      "/etc/resolv.conf",
			},
		},
		MaskPaths: []string{
			"/proc/kcore",
			"/proc/latency_stats",
			"/proc/timer_stats",
		},
		ReadonlyPaths: []string{
			"/proc/asound",
			"/proc/bus",
			"/proc/fs",
			"/proc/irq",
			"/proc/sys",
			"/proc/sysrq-trigger",
		},
		MountLabel: containerUUID,
		Devices:    configs.DefaultSimpleDevices,
		PivotDir:   "/tmp",
		Rootfs:     filepath.Join(dataPath, "root"),
		Readonlyfs: true,
		Hostname:   containerUUID,
		Namespaces: []configs.Namespace{
			{Type: configs.NEWIPC},
			{Type: configs.NEWNET}, // TODO: SandboxKey from libnetwork
			{Type: configs.NEWNS},
			{Type: configs.NEWPID},
			{Type: configs.NEWUTS},
		},
		Capabilities: []string{
			"CHOWN",
			"DAC_OVERRIDE",
			"FSETID",
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
			"AUDIT_WRITE",
		},
		Networks: []*configs.Network{
			{
				Address: "127.0.0.1/0",
				Gateway: "localhost",
				Mtu:     1500,
				Type:    "loopback",
			},
			//network,
		},
		Cgroups: &configs.Cgroup{
			Name:           containerUUID,
			AllowedDevices: configs.DefaultAllowedDevices,
		},
		// TODO: resource limits (cgroups and rlimits)
		// TODO: sysctl set somaxconn
	}
}
