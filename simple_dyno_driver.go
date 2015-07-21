package hsup

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/heroku/hsup/diag"
)

type SimpleDynoDriver struct {
}

func (dd *SimpleDynoDriver) Build(release *Release) error {
	return nil
}

func (dd *SimpleDynoDriver) Start(ex *Executor) (err error) {
	args := []string{"bash", "-c"}
	args = append(args, ex.Args...)
	ex.cmd = exec.Command(args[0], args[1:]...)

	ex.cmd.Stdin = os.Stdin
	ex.cmd.Stdout = os.Stdout
	ex.cmd.Stderr = os.Stderr

	// Tee stdout and stderr to Logplex.
	if ex.LogplexURL != nil {
		var rStdout, rStderr io.ReadCloser
		rStdout, ex.cmd.Stdout = teePipe(os.Stdout)
		rStderr, ex.cmd.Stderr = teePipe(os.Stderr)
		if ex.logsRelay, err = newRelay(
			ex.LogplexURL, ex.Name(), rStdout, rStderr,
		); err != nil {
			return err
		}
		ex.logsRelay.run()
	}

	// Fill environment vector from Heroku configuration.
	for k, v := range ex.Release.config {
		ex.cmd.Env = append(ex.cmd.Env, k+"="+v)
	}

	if ex.IPInfo, err = DefaultIPInfo(ex); err != nil {
		return err
	}

	ex.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = ex.cmd.Start()
	if err != nil {
		return err
	}

	ex.waiting = make(chan struct{})

	return nil
}

func (dd *SimpleDynoDriver) Wait(ex *Executor) (s *ExitStatus) {
	s = &ExitStatus{}
	err := ex.cmd.Wait()
	if err != nil {
		if eErr, ok := err.(*exec.ExitError); ok {
			if status, ok := eErr.Sys().(syscall.WaitStatus); ok {
				s.Code = status.ExitStatus()
				if status.Signaled() {
					s.Code = 128 + int(status.Signal())
				}
			}
		} else {
			// Non ExitErrors are propagated: they are
			// liable to be errors in starting the
			// process.
			s.Err = err
		}
	}

	if ex.logsRelay != nil {
		// wait until all buffered logs are delivered
		ex.logsRelay.stop()
	}
	go func() {
		ex.waiting <- struct{}{}
	}()

	return s
}

func (dd *SimpleDynoDriver) Stop(ex *Executor) error {
	p := ex.cmd.Process

	group, err := os.FindProcess(-1 * p.Pid)
	if err != nil {
		return err
	}

	// Begin graceful shutdown via SIGTERM.
	group.Signal(syscall.SIGTERM)

	for {
		select {
		case <-time.After(10 * time.Second):
			diag.Log("sigkill", group)
			group.Signal(syscall.SIGKILL)
		case <-ex.waiting:
			diag.Log("waited", group)
			return nil
		}
		diag.Log("spin", group)
		time.Sleep(1)
	}
}
