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
	"time"

	"code.google.com/p/go-uuid/uuid"

	"github.com/docker/libnetwork"
	"github.com/opencontainers/runc/libcontainer"
	"github.com/opencontainers/runc/libcontainer/configs"
)

var (
	dynoPrivateSubnet net.IPNet
	extraIFHost       string
	extraIFIP         net.IPNet
	extraRoutes       []*configs.Route

	// default to max 8K dynos
	dynoMinUID int = 3000
	dynoMaxUID int = 11000
)

type LibContainerDynoDriver struct {
	workDir       string
	stacksDir     string
	containersDir string
	allocator     *Allocator

	controller     libnetwork.NetworkController
	primaryNetwork libnetwork.Network
	extraNetwork   libnetwork.Network
	extraRoutes    []*configs.Route
}

type InvalidExtraIFErr struct {
	value string
}

func (e *InvalidExtraIFErr) String() string {
	return e.value
}

func (e *InvalidExtraIFErr) Error() string {
	return fmt.Sprintf(
		"Invalid LIBCONTAINER_DYNO_EXTRA_INTERFACE: %q is not in the hostIFName:x.y.w.z/n format",
		e.value,
	)
}

// init reads custom configuration from ENV vars:
// - LIBCONTAINER_DYNO_SUBNET
// - LIBCONTAINER_DYNO_EXTRA_INTERFACE
// - LIBCONTAINER_DYNO_EXTRA_ROUTES
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

	if extraIF := strings.TrimSpace(
		os.Getenv("LIBCONTAINER_DYNO_EXTRA_INTERFACE"),
	); len(extraIF) > 0 {
		parts := strings.SplitN(extraIF, ":", 2)
		if len(parts) != 2 {
			panic(&InvalidExtraIFErr{extraIF})
		}
		ip, subnet, err := net.ParseCIDR(parts[1])
		if err != nil {
			panic(&InvalidExtraIFErr{extraIF})
		}
		extraIFHost = parts[0]
		extraIFIP = net.IPNet{
			IP:   ip.To4(),
			Mask: subnet.Mask,
		}
	}

	extraRoutes = make([]*configs.Route, 0)
	if extraRoutesS := strings.TrimSpace(
		os.Getenv("LIBCONTAINER_DYNO_EXTRA_ROUTES"),
	); len(extraRoutesS) > 0 {
		// dest:gateway:ifName,dest:gateway:ifName,...
		routes := strings.Split(extraRoutesS, ",")
		for _, r := range routes {
			destGwIf := strings.Split(r, ":")
			if len(destGwIf) != 3 {
				panic("incorrect item count in " + r)
			}
			dest, gw, ifName := destGwIf[0], destGwIf[1], destGwIf[2]
			_, _, err := net.ParseCIDR(dest)
			if err != nil {
				panic("invalid destination " + dest)
			}
			if net.ParseIP(gw) == nil {
				panic("invalid gateway " + gw)
			}
			extraRoutes = append(extraRoutes, &configs.Route{
				Destination:   dest,
				Gateway:       gw,
				InterfaceName: ifName,
			})
		}
	}

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
	controller, err := libnetwork.New()
	if err != nil {
		return nil, err
	}
	primaryNetwork, extraNetwork, err := dynoNetworks(controller)
	if err != nil {
		return nil, err
	}

	return &LibContainerDynoDriver{
		workDir:        workDir,
		stacksDir:      stacksDir,
		containersDir:  containersDir,
		allocator:      allocator,
		controller:     controller,
		primaryNetwork: primaryNetwork,
		extraNetwork:   extraNetwork,
		extraRoutes:    extraRoutes,
	}, nil
}

func dynoNetworks(
	controller libnetwork.NetworkController,
) (primary libnetwork.Network, extra libnetwork.Network, err error) {
	if err := RegisterRoutedDriver(controller); err != nil {
		return nil, nil, err
	}
	dynoSubnetOpt := libnetwork.NetworkOptionGeneric(map[string]interface{}{
		"subnet": dynoPrivateSubnet,
	})
	if primary, err = controller.NewNetwork(
		"routed", "dynos", dynoSubnetOpt,
	); err != nil {
		return nil, nil, err
	}

	if len(extraIFHost) == 0 {
		return primary, nil, nil
	}

	if err := RegisterMacvlanDriver(controller); err != nil {
		return nil, nil, err
	}
	extraIFOpts := libnetwork.NetworkOptionGeneric(map[string]interface{}{
		"hostIF": extraIFHost,
	})
	if extra, err = controller.NewNetwork(
		"macvlan", "dynosExtra", extraIFOpts,
	); err != nil {
		return nil, nil, err
	}
	return primary, extra, nil
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

func (dd *LibContainerDynoDriver) networkFor(uid int) (*SmallSubnet, error) {
	sn, err := dd.allocator.privateNetForUID(uid)
	if err != nil {
		return nil, err
	}
	subnet, err := NewSmallSubnet(sn)
	if err != nil {
		return nil, err
	}
	return subnet, nil
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
		return network.Host().IP.String(), port
	}

	addressOpt := map[string]interface{}{
		"subnet": network,
	}
	endpoint, err := dd.primaryNetwork.CreateEndpoint(containerUUID,
		libnetwork.EndpointOptionGeneric(addressOpt),
	)
	if err != nil {
		return err
	}
	if err := endpoint.Join(containerUUID,
		libnetwork.JoinOptionHostname(containerUUID),
	); err != nil {
		return err
	}
	var extraEndpoint libnetwork.Endpoint
	if dd.extraNetwork != nil {
		extraAddressOpt := map[string]interface{}{
			"address": extraIFIP,
		}
		if extraEndpoint, err = dd.extraNetwork.CreateEndpoint(containerUUID,
			libnetwork.EndpointOptionGeneric(extraAddressOpt),
		); err != nil {
			return err
		}
		if err := extraEndpoint.Join(containerUUID); err != nil {
			return err
		}
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
			if err := dd.controller.LeaveAll(containerUUID); err != nil {
				log.Printf("controller.LeaveAll error: %#+v", err)
			}
			if err := endpoint.Delete(); err != nil {
				log.Printf("endpoint.Leave error: %#+v", err)
			}
			if extraEndpoint != nil {
				if err := extraEndpoint.Delete(); err != nil {
					log.Printf("extraEndpoint.Leave error: %#+v", err)
				}
			}
			netGCDone := make(chan struct{}, 1)
			go func() {
				dd.controller.GC()
				netGCDone <- struct{}{}
			}()
			// wait at most 10s
			select {
			case <-netGCDone:
			case <-time.After(10 * time.Second):
			}

			// it's probably safe to ignore errors here. Worst case
			// scenario, this uid won't be be reused.
			dd.allocator.FreeUID(uid)

			var ec int
			if code != nil {
				ec = code.Sys().(syscall.WaitStatus).ExitStatus()
			}

			// TODO: handle exit status when signaled
			ex.initExitStatus <- &ExitStatus{
				Code: ec,
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
	container, err = factory.Create(
		containerUUID, containerConfig(
			containerUUID,
			dataPath,
			endpoint.Info().SandboxKey(),
			dd.extraRoutes,
		),
	)
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

func containerConfig(containerUUID, dataPath, netNS string, routes []*configs.Route) *configs.Config {
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
			{Type: configs.NEWPID},
			{Type: configs.NEWNS},
			{Type: configs.NEWUTS},
			{Type: configs.NEWIPC},
			{Type: configs.NEWNET, Path: netNS},
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
		Routes: routes,
		Cgroups: &configs.Cgroup{
			Name:           containerUUID,
			AllowedDevices: configs.DefaultAllowedDevices,
		},
		// TODO: resource limits (cgroups and rlimits)
		// TODO: sysctl set somaxconn
	}
}
