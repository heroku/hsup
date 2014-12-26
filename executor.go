package main

import (
	"errors"
	"log"
	"os/exec"

	"github.com/fsouza/go-dockerclient"
)

type DynoState int

const (
	// Pseudo-states used for goal-state communication.
	NonState DynoState = iota
	Restarting

	Stopped
	Started
	Retiring
	Retired
)

var ExecutorComplete = errors.New("Executor complete")

type Executor struct {
	argv        []string
	dynoDriver  DynoDriver
	release     *Release
	processID   string
	processType string
	complete    chan struct{}

	// simple dyno driver properties
	cmd     *exec.Cmd
	waiting chan struct{}

	// docker dyno driver properties
	container *docker.Container

	// FSM Fields
	state    DynoState
	lastGoal DynoState
	needTick chan DynoState
}

func (e *Executor) Trigger(goal DynoState) {
	log.Println("triggering", goal)
	select {
	case e.needTick <- goal:
	case <-e.complete:
	}
}

func (e *Executor) Tick() (err error) {
	log.Println(e.Name(), "waiting for tick...", e.state, e.lastGoal)
	if goal := <-e.needTick; goal != NonState {
		e.lastGoal = goal
		log.Println(e.Name(), "ticking...", e.state, e.lastGoal)
	}

	start := func() error {
		log.Printf("%v: starting\n", e.Name())
		if err = e.dynoDriver.Start(e); err != nil {
			log.Printf("%v: start fails: %v", e.Name(), err)
			go e.Trigger(Restarting)
			return err
		}

		log.Printf("%v: started\n", e.Name())
		e.state = Started
		e.lastGoal = NonState

		go func() {
			e.dynoDriver.Wait(e)
			e.Trigger(Restarting)
		}()

		return nil
	}

	s := e.state
	switch s {
	case Retired:
		return ExecutorComplete
	case Retiring:
		if err = e.dynoDriver.Stop(e); err != nil {
			return err
		}

		e.state = Retired

		return ExecutorComplete
	case Stopped:
		switch e.lastGoal {
		case Retired:
			e.state = Retired
			go e.Trigger(NonState)
			return nil
		case Started:
			fallthrough
		case Restarting:
			return start()
		default:
			panic("Invalid goal state")
		}
	case Started:
		switch e.lastGoal {
		case Retired:
			e.state = Retiring
			go e.Trigger(NonState)
			return nil
		case Restarting:
			e.state = Stopped
			go e.Trigger(NonState)
			return nil
		case Started:
			return nil
		default:
			panic("Invalid goal state")
		}
	default:
		panic("Invalid state")
	}
}

func (e *Executor) Name() string {
	return e.processType + "." + e.processID
}
