package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type SimpleDynoDriver struct {
	cmd     *exec.Cmd
	waiting chan error
}

func (dd *SimpleDynoDriver) Build(release *Release) error {
	return nil
}

func (dd *SimpleDynoDriver) Start(ex *Executor) error {
	dd.cmd = exec.Command(ex.argv[0], ex.argv[1:]...)

	dd.cmd.Stdin = os.Stdin
	dd.cmd.Stdout = os.Stdout
	dd.cmd.Stderr = os.Stderr

	// Fill environment vector from Heroku configuration.
	for k, v := range ex.release.config {
		dd.cmd.Env = append(dd.cmd.Env, k+"="+v)
	}

	dd.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err := dd.cmd.Start()
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

func (dd *SimpleDynoDriver) Stop(ex *Executor) error {
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
			return err
		}
		log.Println("spin", group)
		time.Sleep(1)
	}
}
