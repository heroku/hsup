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
	args        []string
	dynoDriver  DynoDriver
	release     *Release
	processID   string
	processType string
	Status      chan *ExitStatus
	complete    chan struct{}

	// simple dyno driver properties
	cmd     *exec.Cmd
	waiting chan struct{}

	// docker dyno driver properties
	container *docker.Container

	// FSM Fields
	OneShot  bool
	state    DynoState
	newInput chan DynoInput
}

func (e *Executor) Trigger(input DynoInput) {
	log.Println("triggering", input)
	select {
	case e.newInput <- input:
	case <-e.complete:
	}
}

func (e *Executor) wait() {
	if s := e.dynoDriver.Wait(e); e.Status != nil {
		log.Println("Executor exits:", e.Name(), "exit code:", s.Code)
		e.Status <- s
	}
	e.Trigger(Exited)
}

func (e *Executor) Tick() (err error) {
	log.Println(e.Name(), "waiting for tick...", e.state)
	input := <-e.newInput
	log.Println(e.Name(), "ticking with input", input)

	start := func() error {
		log.Printf("%v: starting\n", e.Name())
		if err = e.dynoDriver.Start(e); err != nil {
			log.Printf("%v: start fails: %v", e.Name(), err)
			if e.OneShot {
				go e.Trigger(Retire)
			} else {
				go e.Trigger(Restart)
			}
			return err
		}

		log.Printf("%v: started\n", e.Name())
		e.state = Started
		go e.wait()
		return nil
	}

again:
	switch e.state {
	case Retired:
		close(e.complete)
		return ErrExecutorComplete
	case Retiring:
		if err = e.dynoDriver.Stop(e); err != nil {
			return err
		}

		e.state = Retired
		goto again
	case Stopped:
		switch input {
		case Retire:
			e.state = Retired
			goto again
		case Exited:
			if e.OneShot {
				e.state = Retired
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
			e.state = Retiring
			goto again
		case Exited:
			e.state = Stopped
			goto again
		case Restart:
			if err = e.dynoDriver.Stop(e); err != nil {
				return err
			}
			goto again
		default:
			panic(fmt.Sprintln("Invalid input", input))
		}
	default:
		panic(fmt.Sprintln("Invalid state", e.state))
	}
}

func (e *Executor) Name() string {
	return e.processType + "." + e.processID
}
