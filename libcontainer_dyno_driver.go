// +build linux

package hsup

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"code.google.com/p/go-uuid/uuid"

	"github.com/docker/libcontainer"
	"github.com/docker/libcontainer/cgroups"
	"github.com/docker/libcontainer/devices"
	"github.com/docker/libcontainer/mount"
	"github.com/docker/libcontainer/namespaces"
)

var (
	// 172.16.0.28/30
	basePrivateIP = net.IPNet{
		IP:   net.IPv4(172, 16, 0, 28).To4(),
		Mask: net.CIDRMask(30, 32),
	}
)

type LibContainerDynoDriver struct {
	workDir       string
	stacksDir     string
	containersDir string
	uidsDir       string

	// (maxUID-minUID) should always be smaller than 2 ** 18
	// see privateNetForUID for details
	minUID int
	maxUID int

	rng *rand.Rand
}

func NewLibContainerDynoDriver(workDir string) (*LibContainerDynoDriver, error) {
	var (
		stacksDir     = filepath.Join(workDir, "stacks")
		containersDir = filepath.Join(workDir, "containers")
		uidsDir       = filepath.Join(workDir, "uids")
	)
	if err := os.MkdirAll(stacksDir, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(containersDir, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(uidsDir, 0755); err != nil {
		return nil, err
	}

	// use a seed with some entropy from crypt/rand to initialize a cheaper
	// math/rand rng
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewSource(seed.Int64()))

	return &LibContainerDynoDriver{
		workDir:       workDir,
		stacksDir:     stacksDir,
		containersDir: containersDir,
		uidsDir:       uidsDir,
		minUID:        3000,
		maxUID:        60000,
		rng:           rng,
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
	uid, gid, err := dd.findFreeUIDGID()
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
		if err := os.Chown(path, uid, gid); err != nil {
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
		stackImagePath, dataPath, uid, gid,
	); err != nil {
		return err
	}

	// TODO: inject /tmp/slug.tgz if local

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
		ex:             ex,
		configPipe:     cfgReader,
	}
	container := containerConfig(
		containerUUID, uid, gid, dataPath, ex.Release.ConfigSlice(),
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

		// free the UID
		uidFile := filepath.Join(dd.uidsDir, strconv.Itoa(uid))
		// it's probably safe to ignore errors here, the file is
		// probably gone. Worst case scenario, this uid won't be be
		// reused.
		os.Remove(uidFile)

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

// findFreeUIDGID optimistically locks uid and gid pairs until one is
// successfully allocated. It relies on atomic filesystem operations to
// guarantee that multiple concurrent tasks will never allocate the same uid/gid
// pair.
func (dd *LibContainerDynoDriver) findFreeUIDGID() (int, int, error) {
	var (
		interval   = dd.maxUID - dd.minUID + 1
		maxRetries = 5 * interval
	)
	// try random uids in the [minUID, maxUID] interval until one works.
	// With a good random distribution, a few times the number of possible
	// uids should be enough attempts to guarantee that all possible uids
	// will be eventually tried.
	for i := 0; i < maxRetries; i++ {
		uid := dd.rng.Intn(interval) + dd.minUID
		uidFile := filepath.Join(dd.uidsDir, strconv.Itoa(uid))
		// check if free by optimistically locking this uid
		f, err := os.OpenFile(uidFile, os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			continue // already allocated by someone else
		}
		if err := f.Close(); err != nil {
			return 0, 0, err
		}
		return uid, uid, nil
	}
	return 0, 0, errors.New("no free UID available")
}

// privateNetworkForUID determines which /30 IPv4 network to use for each
// container, relying on the fact that each one has a different, unique UID
// allocated to them.
//
// All /30 subnets are allocated from the 172.16/12 block (RFC1918 - Private
// Address Space), starting at 172.16.0.28/30 to avoid clashes with IPs used by
// AWS (eg.: the internal DNS server is 172.16.0.23 on ec2-classic). This block
// provides at most 2**18 = 262144 subnets of size /30, then (maxUID-minUID)
// must be always smaller than 262144.
func (dd *LibContainerDynoDriver) privateNetForUID(uid int) (*net.IPNet, error) {
	shift := uint32(uid - dd.minUID)
	var asInt uint32
	base := bytes.NewReader(basePrivateIP.IP.To4())
	if err := binary.Read(base, binary.BigEndian, &asInt); err != nil {
		return nil, err
	}

	// pick a /30 block
	asInt >>= 2
	asInt += shift
	asInt <<= 2

	var ip bytes.Buffer
	if err := binary.Write(&ip, binary.BigEndian, &asInt); err != nil {
		return nil, err
	}
	return &net.IPNet{
		IP:   net.IP(ip.Bytes()),
		Mask: basePrivateIP.Mask,
	}, nil
}

func createPasswdWithDynoUser(stackImagePath, dataPath string, uid, gid int) error {
	var contents bytes.Buffer
	original, err := os.Open(filepath.Join(stackImagePath, "etc", "passwd"))
	if err != nil {
		return err
	}
	defer original.Close()

	if _, err := contents.ReadFrom(original); err != nil {
		return err
	}
	// TODO: allocate a free uid. It is currently hardcoded to 1000
	dynoUser := fmt.Sprintf("\ndyno:x:%d:%d::/app:/bin/bash\n", uid, gid)
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

	//child process is already running, it's safe to close the parent's read
	//side of the pipe
	ctx.configPipe.Close()
}

func containerConfig(
	containerUUID string,
	uid, gid int,
	dataPath string,
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
		User:     fmt.Sprintf("%d:%d", uid, gid),
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
			// TODO: setup our own network instead of using the docker bridge
			{
				Address:    "172.17.0.101/16",
				Bridge:     "docker0",
				VethPrefix: "veth",
				Gateway:    "172.17.42.1",
				Mtu:        1500,
				Type:       "veth",
			},
		},
		Cgroups: &cgroups.Cgroup{
			Name:           containerUUID,
			AllowedDevices: devices.DefaultAllowedDevices,
		},
	}
}
