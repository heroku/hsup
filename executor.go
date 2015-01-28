//go:generate stringer -type=DynoState,DynoInput
package hsup

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"

	"github.com/fsouza/go-dockerclient"
	"net/url"
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
	ProcessID   int
	ProcessType string
	Status      chan *ExitStatus
	Complete    chan struct{}
	LogplexURL  *url.URL

	// simple, abspath, and libcontainer dyno driver properties
	cmd     *exec.Cmd
	waiting chan struct{}

	// docker dyno driver properties
	container *docker.Container

	// libcontainer dyno driver properties
	lcStatus      chan *ExitStatus
	waitStartup   chan struct{}
	waitWait      chan struct{}
	containerUUID string

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
			log.Printf("%v: start fails: %q", e.Name(), err.Error())
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
		switch input {
		case Exited:
			e.State = Retired
			goto again
		case Retire:
			return e.DynoDriver.Stop(e)
		default:
			return nil
		}
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
			return e.DynoDriver.Stop(e)
		default:
			panic(fmt.Sprintln("Invalid input", input))
		}
	default:
		panic(fmt.Sprintln("Invalid state", e.State))
	}
}

func (e *Executor) Name() string {
	return e.ProcessType + "." + strconv.Itoa(e.ProcessID)
}

// Convenience function to return an empty LogplexURL string when
// there *url.URL is nil.
//
// This is necessitated because of the repeated conversions between
// url.URL and string when dealing with hsup.Startup serialization
// constraints.
func (e *Executor) logplexURLString() string {
	if e.LogplexURL == nil {
		return ""
	}

	return e.LogplexURL.String()
}
