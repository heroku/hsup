package hsup

import (
	"errors"
	"log"
	"os/exec"

	"fmt"
	"github.com/fsouza/go-dockerclient"
)

type DynoState int

const (
	Stopped DynoState = iota
	Started
	Retiring
	Retired
)

type DynoInput int

const (
	Retire DynoInput = iota
	Restart
	Exited
	StayStarted
)

var ErrExecutorComplete = errors.New("Executor complete")

type Executor struct {
	Args        []string
	DynoDriver  DynoDriver
	Release     *Release
	ProcessID   string
	ProcessType string
	Status      chan *ExitStatus
	Complete    chan struct{}

	// simple, abspath, and libcontainer dyno driver properties
	cmd     *exec.Cmd
	waiting chan struct{}

	// docker dyno driver properties
	container *docker.Container

	// libcontainer dyno driver properties
	lcStatus    chan *ExitStatus
	waitStartup chan struct{}
	waitWait    chan struct{}

	// FSM Fields
	OneShot  bool
	State    DynoState
	NewInput chan DynoInput
}

func (e *Executor) Trigger(input DynoInput) {
	log.Println("triggering", input)
	select {
	case e.NewInput <- input:
	case <-e.Complete:
	}
}

func (e *Executor) wait() {
	if s := e.DynoDriver.Wait(e); e.Status != nil {
		log.Println("Executor exits:", e.Name(), "exit code:", s.Code)
		e.Status <- s
	}
	e.Trigger(Exited)
}

func (e *Executor) Tick() (err error) {
	log.Println(e.Name(), "waiting for tick...", e.State)
	input := <-e.NewInput
	log.Println(e.Name(), "ticking with input", input)

	start := func() error {
		log.Printf("%v: starting\n", e.Name())
		if err = e.DynoDriver.Start(e); err != nil {
			log.Printf("%v: start fails: %v", e.Name(), err)
			if e.OneShot {
				go e.Trigger(Retire)
			} else {
				go e.Trigger(Restart)
			}
			return err
		}

		log.Printf("%v: started\n", e.Name())
		e.State = Started
		go e.wait()
		return nil
	}

again:
	switch e.State {
	case Retired:
		close(e.Complete)
		return ErrExecutorComplete
	case Retiring:
		if err = e.DynoDriver.Stop(e); err != nil {
			return err
		}

		e.State = Retired
		goto again
	case Stopped:
		switch input {
		case Retire:
			e.State = Retired
			goto again
		case Exited:
			if e.OneShot {
				e.State = Retired
				goto again
			}

			return start()
		case StayStarted:
			fallthrough
		case Restart:
			return start()
		default:
			panic(fmt.Sprintln("Invalid input", input))
		}
	case Started:
		switch input {
		case Retire:
			e.State = Retiring
			goto again
		case Exited:
			e.State = Stopped
			goto again
		case Restart:
			if err = e.DynoDriver.Stop(e); err != nil {
				return err
			}
			goto again
		default:
			panic(fmt.Sprintln("Invalid input", input))
		}
	default:
		panic(fmt.Sprintln("Invalid state", e.State))
	}
}

func (e *Executor) Name() string {
	return e.ProcessType + "." + e.ProcessID
}
