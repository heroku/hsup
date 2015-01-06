package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/cyberdelia/heroku-go/v3"
	flag "github.com/ogier/pflag"
)

var ErrNoReleases = errors.New("No releases found")

type ApiPoller struct {
	Cl  *heroku.Service
	App string
	Dd  DynoDriver

	lastReleaseID string
}

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

type ApiFormation struct {
	h *heroku.Formation
}

func (f *ApiFormation) Args() []string {
	return []string{f.h.Command}
}
func (f *ApiFormation) Quantity() int {
	return f.h.Quantity
}

func (f *ApiFormation) Type() string {
	return f.h.Type
}

func (ap *ApiPoller) fetchLatest() (*heroku.Release, error) {
	releases, err := ap.Cl.ReleaseList(
		ap.App, &heroku.ListRange{Descending: true, Field: "version",
			Max: 1})
	if err != nil {
		return nil, err
	}

	if len(releases) < 1 {
		return nil, ErrNoReleases
	}

	return releases[0], nil
}

func (ap *ApiPoller) fillProcesses(rel *heroku.Release) (*Processes, error) {
	config, err := ap.Cl.ConfigVarInfo(ap.App)
	if err != nil {
		return nil, err
	}

	slug, err := ap.Cl.SlugInfo(ap.App, rel.Slug.ID)
	if err != nil {
		return nil, err
	}

	hForms, err := ap.Cl.FormationList(ap.App, &heroku.ListRange{})
	if err != nil {
		return nil, err
	}

	procs := Processes{
		r: &Release{
			appName: ap.App,
			config:  config,
			slugURL: slug.Blob.URL,
			version: rel.Version,
		},
		forms: make([]Formation, len(hForms), len(hForms)),
		dd:    ap.Dd,
	}

	for i, hForm := range hForms {
		procs.forms[i] = &ApiFormation{h: hForm}
	}

	return &procs, nil
}

func (ap *ApiPoller) pollOnce() (*Processes, error) {
	release, err := ap.fetchLatest()
	if err != nil {
		return nil, err
	}

	if release != nil && ap.lastReleaseID != release.ID {
		ap.lastReleaseID = release.ID

		log.Printf("New release %s detected", ap.lastReleaseID)
		return ap.fillProcesses(release)
	}

	return nil, nil
}

func (ap *ApiPoller) pollSynchronous(out chan<- *Processes) {
	for {
		procs, err := ap.pollOnce()
		if err != nil {
			log.Println("Could not fetch new release information:",
				err)
			goto wait
		}

		if procs != nil {
			out <- procs
		}

	wait:
		time.Sleep(10 * time.Second)
	}
}

// Listens for new releases by periodically polling the Heroku
// API. When a new release is detected it is sent to the returned
// channel.
func (ap *ApiPoller) poll() <-chan *Processes {
	out := make(chan *Processes)
	go ap.pollSynchronous(out)
	return out
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
	procs := poller.poll()
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
					log.Println("could not execute", eName,
						":", s.err)
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
