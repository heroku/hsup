// +build linux

package namespaces

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"syscall"

	"github.com/docker/libcontainer"
	"github.com/docker/libcontainer/apparmor"
	"github.com/docker/libcontainer/console"
	"github.com/docker/libcontainer/ipc"
	"github.com/docker/libcontainer/label"
	"github.com/docker/libcontainer/mount"
	"github.com/docker/libcontainer/network"
	"github.com/docker/libcontainer/security/restrict"
	"github.com/docker/libcontainer/system"
	"github.com/docker/libcontainer/utils"
)

func InitReturn(container *libcontainer.Config, uncleanRootfs, consolePath string, pipe *os.File) (err error) {
	defer func() {
		// if we have an error during the initialization of the container's init then send it back to the
		// parent process in the form of an initError.
		if err != nil {
			// ensure that any data sent from the parent is consumed so it doesn't
			// receive ECONNRESET when the child writes to the pipe.
			ioutil.ReadAll(pipe)
			if err := json.NewEncoder(pipe).Encode(initError{
				Message: err.Error(),
			}); err != nil {
				panic(err)
			}
		}
		// ensure that this pipe is always closed
		pipe.Close()
	}()

	rootfs, err := utils.ResolveRootfs(uncleanRootfs)
	if err != nil {
		return err
	}

	// clear the current processes env and replace it with the environment
	// defined on the container
	if err := LoadContainerEnvironment(container); err != nil {
		return err
	}

	// We always read this as it is a way to sync with the parent as well
	var networkState *network.NetworkState
	if err := json.NewDecoder(pipe).Decode(&networkState); err != nil {
		return err
	}

	if consolePath != "" {
		if err := console.OpenAndDup(consolePath); err != nil {
			return err
		}
	}
	if _, err := syscall.Setsid(); err != nil {
		return fmt.Errorf("setsid %s", err)
	}
	if consolePath != "" {
		if err := system.Setctty(); err != nil {
			return fmt.Errorf("setctty %s", err)
		}
	}
	if err := ipc.Initialize(container.IpcNsPath); err != nil {
		return fmt.Errorf("setup IPC %s", err)
	}
	if err := setupNetwork(container, networkState); err != nil {
		return fmt.Errorf("setup networking %s", err)
	}
	if err := setupRoute(container); err != nil {
		return fmt.Errorf("setup route %s", err)
	}

	if err := setupRlimits(container); err != nil {
		return fmt.Errorf("setup rlimits %s", err)
	}

	label.Init()

	if err := mount.InitializeMountNamespace(rootfs,
		consolePath,
		container.RestrictSys,
		(*mount.MountConfig)(container.MountConfig)); err != nil {
		return fmt.Errorf("setup mount namespace %s", err)
	}

	if container.Hostname != "" {
		if err := syscall.Sethostname([]byte(container.Hostname)); err != nil {
			return fmt.Errorf("unable to sethostname %q: %s", container.Hostname, err)
		}
	}

	if err := apparmor.ApplyProfile(container.AppArmorProfile); err != nil {
		return fmt.Errorf("set apparmor profile %s: %s", container.AppArmorProfile, err)
	}

	if err := label.SetProcessLabel(container.ProcessLabel); err != nil {
		return fmt.Errorf("set process label %s", err)
	}

	// TODO: (crosbymichael) make this configurable at the Config level
	if container.RestrictSys {
		if err := restrict.Restrict("proc/sys", "proc/sysrq-trigger", "proc/irq", "proc/bus"); err != nil {
			return err
		}
	}

	pdeathSignal, err := system.GetParentDeathSignal()
	if err != nil {
		return fmt.Errorf("get parent death signal %s", err)
	}

	if err := FinalizeNamespace(container); err != nil {
		return fmt.Errorf("finalize namespace %s", err)
	}

	// FinalizeNamespace can change user/group which clears the parent death
	// signal, so we restore it here.
	if err := RestoreParentDeathSignal(pdeathSignal); err != nil {
		return fmt.Errorf("restore parent death signal %s", err)
	}

	return nil
}
