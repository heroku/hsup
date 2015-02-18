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

	// simple and abspath dyno driver properties
	cmd       *exec.Cmd
	waiting   chan struct{}
	logsRelay *relay

	// docker dyno driver properties
	container *docker.Container

	// libcontainer dyno driver properties
	initExitStatus chan *ExitStatus

	// FSM Fields
	OneShot  bool
	State    DynoState
	NewInput chan DynoInput

	// Status API fields
	IPAddress string
	Port      int
}

func (ex *Executor) dlog(values ...interface{}) {
	diag.Log(append(
		[]interface{}{"Executor", ex.Name(), fmt.Sprintf("%p", ex)},
		values...)...)
}

func (ex *Executor) Trigger(input DynoInput) {
	ex.dlog("trigger", input)
	select {
	case ex.NewInput <- input:
	case <-ex.Complete:
	}
}

func (ex *Executor) wait() {
	if s := ex.DynoDriver.Wait(ex); ex.Status != nil {
		log.Println("Executor exits:", ex.Name(), "exit code:", s.Code)
		ex.Status <- s
	}
	ex.Trigger(Exited)
}

func (ex *Executor) Tick() (err error) {
	ex.dlog("waiting for tick... (current state:", ex.State.String()+")")
	input := <-ex.NewInput
	ex.dlog("ticking with input", ex.State)

	start := func() error {
		log.Printf("%v: starting\n", ex.Name())
		if err = ex.DynoDriver.Start(ex); err != nil {
			log.Printf("%v: start fails: %q", ex.Name(), err.Error())
			if ex.OneShot {
				go ex.Trigger(Retire)
			} else {
				go ex.Trigger(Restart)
			}
			return err
		}

		ex.dlog("started")
		ex.State = Started
		go ex.wait()
		return nil
	}

again:
	switch ex.State {
	case Retired:
		close(ex.Complete)
		return ErrExecutorComplete
	case Retiring:
		switch input {
		case Exited:
			ex.State = Retired
			goto again
		case Retire:
			return ex.DynoDriver.Stop(ex)
		default:
			return nil
		}
	case Stopped:
		switch input {
		case Retire:
			ex.State = Retired
			goto again
		case Exited:
			if ex.OneShot {
				ex.State = Retired
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
			ex.State = Retiring
			goto again
		case Exited:
			ex.State = Stopped
			goto again
		case Restart:
			return ex.DynoDriver.Stop(ex)
		default:
			panic(fmt.Sprintln("Invalid input", input))
		}
	default:
		panic(fmt.Sprintln("Invalid state", ex.State))
	}
}

func (ex *Executor) Name() string {
	return ex.ProcessType + "." + strconv.Itoa(ex.ProcessID)
}

// Convenience function to return an empty LogplexURL string when
// there *url.URL is nil.
//
// This is necessitated because of the repeated conversions between
// url.URL and string when dealing with hsup.Startup serialization
// constraints.
func (ex *Executor) logplexURLString() string {
	if ex.LogplexURL == nil {
		return ""
	}

	return ex.LogplexURL.String()
}

func (ex *Executor) bindPairs() []string {
	pairs := make([]string, len(ex.Binds))

	i := 0
	for k, v := range ex.Binds {
		pairs[i] = fmt.Sprintf("%s:%s", k, v)
		i++
	}

	return pairs
}
