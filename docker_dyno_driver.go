package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type DockerDynoDriver struct {
	d     *Docker
	state DynoState

	cmd     *exec.Cmd
	waiting chan error
}

func NewDockerDynoDriver() *DockerDynoDriver {
	return &DockerDynoDriver{}
}

func (dd *DockerDynoDriver) State() DynoState {
	return dd.state
}

func (dd *DockerDynoDriver) Start(b *Bundle) error {
	if dd.d == nil {
		dd.d = &Docker{}
		if err := dd.d.Connect(); err != nil {
			dd.d = nil
			return err
		}
	}

	si, err := dd.d.StackStat("cedar-14")
	if err != nil {
		return err
	}

	log.Printf("StackImage %+v", si)
	err = dd.d.BuildSlugImage(si, b.slug.Blob.URL)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Built image successfully")

	dd.state = Started
	dd.cmd = exec.Command(b.argv[0], b.argv[1:]...)

	dd.cmd.Stdin = os.Stdin
	dd.cmd.Stdout = os.Stdout
	dd.cmd.Stderr = os.Stderr

	// Fill environment vector from Heroku configuration.
	for k, v := range b.config {
		dd.cmd.Env = append(dd.cmd.Env, k+"="+v)
	}

	dd.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err = dd.cmd.Start()
	if err != nil {
		return err
	}

	dd.waiting = make(chan error)

	go func() {
		log.Println("wait start")
		dd.waiting <- dd.cmd.Wait()
		log.Println("wait complete")
	}()

	return nil
}

func (dd *DockerDynoDriver) Stop() error {
	// If we could never start the process, don't worry about stopping it. May
	// occur in cases like if Docker was down.
	if dd.cmd == nil {
		return nil
	}

	p := dd.cmd.Process

	group, err := os.FindProcess(-1 * p.Pid)
	if err != nil {
		return err
	}

	// Begin graceful shutdown via SIGTERM.
	group.Signal(syscall.SIGTERM)

	for {
		select {
		case <-time.After(10 * time.Second):
			log.Println("sigkill", group)
			group.Signal(syscall.SIGKILL)
		case err := <-dd.waiting:
			log.Println("waited", group)
			dd.state = Stopped
			return err
		}
		log.Println("spin", group)
		time.Sleep(1)
	}
}
