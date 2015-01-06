package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/cyberdelia/heroku-go/v3"
	flag "github.com/ogier/pflag"
)

var ErrNoReleases = errors.New("No releases found")

type Processes struct {
	r     *Release
	forms []Formation

	dd        DynoDriver
	OneShot   bool
	executors []*Executor
}

func statuses(p *Processes) <-chan []*ExitStatus {
	if p == nil || !p.OneShot {
		return nil
	}

	out := make(chan []*ExitStatus)

	go func() {
		statuses := make([]*ExitStatus, len(p.executors))
		for i, executor := range p.executors {
			log.Println("Got a status")
			statuses[i] = <-executor.status
		}
		out <- statuses
	}()

	return out
}

type Formation interface {
	Args() []string
	Quantity() int
	Type() string
}

func (p *Processes) start(command string, args []string, concurrency int) (
	err error) {
	err = p.dd.Build(p.r)
	if err != nil {
		log.Printf("hsup could not bake image for release %s: %s",
			p.r.Name(), err.Error())
		return err
	}

	if command == "start" {
		for _, form := range p.forms {
			conc := getConcurrency(concurrency, form.Quantity())
			log.Printf("formation quantity=%v type=%v\n",
				conc, form.Type())

			for i := 0; i < conc; i++ {
				executor := &Executor{
					args:        form.Args(),
					dynoDriver:  p.dd,
					processID:   strconv.Itoa(i + 1),
					processType: form.Type(),
					release:     p.r,
					complete:    make(chan struct{}),
					state:       Stopped,
					newInput:    make(chan DynoInput),
				}

				p.executors = append(p.executors, executor)
			}
		}
	} else if command == "run" {
		p.OneShot = true
		conc := getConcurrency(concurrency, 1)
		for i := 0; i < conc; i++ {
			executor := &Executor{
				args:        args,
				dynoDriver:  p.dd,
				processID:   strconv.Itoa(i + 1),
				processType: "run",
				release:     p.r,
				complete:    make(chan struct{}),
				state:       Stopped,
				OneShot:     true,
				status:      make(chan *ExitStatus),
				newInput:    make(chan DynoInput),
			}
			p.executors = append(p.executors, executor)
		}
	}

	p.startParallel()
	return nil
}

func getConcurrency(concurrency int, defaultConcurrency int) int {
	if concurrency == -1 {
		return defaultConcurrency
	}

	return concurrency
}

func main() {
	var err error

	token := os.Getenv("HEROKU_ACCESS_TOKEN")
	if token == "" {
		log.Fatal("need HEROKU_ACCESS_TOKEN")
	}

	heroku.DefaultTransport.Username = ""
	heroku.DefaultTransport.Password = token

	cl := heroku.NewService(heroku.DefaultClient)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s COMMAND [OPTIONS]\n", os.Args[0])
		flag.PrintDefaults()
	}
	appName := flag.StringP("app", "a", "", "app name")
	concurrency := flag.IntP("concurrency", "c", -1,
		"concurrency number")
	dynoDriverName := flag.StringP("dynodriver", "d", "simple",
		"specify a dyno driver (program that starts a program)")
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	switch args[0] {
	case "run":
		if len(args) <= 1 {
			fmt.Fprintln(os.Stderr, "Need a program and arguments "+
				"specified for \"run\".")
			os.Exit(1)
		}
	case "start":
	default:
		fmt.Fprintf(os.Stderr, "Command not found: %v\n", args[0])
		flag.Usage()
		os.Exit(1)
	}

	dynoDriver, err := FindDynoDriver(*dynoDriverName)
	if err != nil {
		log.Fatalln("could not find dyno driver:", *dynoDriverName)
	}

	poller := ApiPoller{Cl: cl, App: *appName, Dd: dynoDriver}
	procs := poller.Poll()
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	var p *Processes
	for {
		select {
		case newProcs := <-procs:
			if p != nil {
				p.stopParallel()
			}
			p = newProcs
			err = p.start(args[0], args[1:], *concurrency)
			if err != nil {
				log.Fatalln("could not start process:", err)
			}
		case statv := <-statuses(p):
			exitVal := 0
			for i, s := range statv {
				eName := p.executors[i].Name()
				if s.err != nil {
					log.Printf("could not execute %s: %s",
						eName, s.err.Error())
					if 255 > exitVal {
						exitVal = 255
					}
				} else {
					log.Println(eName, "exits with code:",
						s.code)
					if s.code > exitVal {
						exitVal = s.code
					}
				}
				os.Exit(exitVal)
			}
			os.Exit(0)
		case sig := <-signals:
			log.Println("hsup caught a deadly signal:", sig)
			if p != nil {
				p.stopParallel()
			}
			os.Exit(1)
		}
	}
}

func (p *Processes) startParallel() {
	for _, executor := range p.executors {
		go func(executor *Executor) {
			go executor.Trigger(StayStarted)
			log.Println("Beginning Tickloop for", executor.Name())
			for executor.Tick() != ErrExecutorComplete {
			}
			log.Println("Executor completes", executor.Name())
		}(executor)
	}
}

// Docker containers shut down slowly, so parallelize this operation
func (p *Processes) stopParallel() {
	log.Println("stopping everything")

	for _, executor := range p.executors {
		go func(executor *Executor) {
			go executor.Trigger(Retire)
		}(executor)
	}

	for _, executor := range p.executors {
		<-executor.complete
	}
}
