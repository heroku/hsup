//go:generate stringer -type=DynoState,DynoInput
package hsup

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os/exec"
	"strconv"

	"github.com/fsouza/go-dockerclient"
	"github.com/heroku/hsup/diag"
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
	Binds       map[string]string

	// simple, abspath, and libcontainer dyno driver properties
	cmd     *exec.Cmd
	waiting chan struct{}

	// docker dyno driver properties
	container *docker.Container

	// libcontainer dyno driver properties
	uid, gid      int
	lcStatus      chan *ExitStatus
	waitStartup   chan struct{}
	waitWait      chan struct{}
	containerUUID string

	// FSM Fields
	OneShot  bool
	State    DynoState
	NewInput chan DynoInput
}

func (e *Executor) dlog(values ...interface{}) {
	diag.Log(append(
		[]interface{}{"Executor", e.Name(), fmt.Sprintf("%p", e)},
		values...)...)
}

func (e *Executor) Trigger(input DynoInput) {
	e.dlog("trigger", fmt.Sprintf("%#v", e), input)
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
	e.dlog("waiting for tick... (current state:", e.State.String()+")")
	input := <-e.NewInput
	e.dlog("ticking with input", e.State)

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

		e.dlog("started")
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

func (e *Executor) bindPairs() []string {
	pairs := make([]string, len(e.Binds))

	i := 0
	for k, v := range e.Binds {
		pairs[i] = fmt.Sprintf("%s:%s", k, v)
		i++
	}

	return pairs
}
