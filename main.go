package main

import (
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

type Processes struct {
	app        string
	cl         *heroku.Service
	dd         DynoDriver
	formations []*Formation
}

type Formation struct {
	release     *heroku.Release
	config      map[string]string
	concurrency int
	argv        []string
	executors   []*Executor
}

func (p *Processes) fetchLatestRelease() (*heroku.Release, error) {
	releases, err := p.cl.ReleaseList(
		p.app, &heroku.ListRange{Descending: true, Field: "version",
			Max: 1})
	if err != nil {
		return nil, err
	}

	if len(releases) < 1 {
		return nil, nil
	}

	return releases[0], nil
}

// Listens for new releases by periodically polling the Heroku
// API. When a new release is detected it is sent to the returned
// channel.
func (p *Processes) startReleasePoll() (
	out <-chan *heroku.Release) {
	lastReleaseID := ""
	releaseChannel := make(chan *heroku.Release)
	go func() {
		for {
			release, err := p.fetchLatestRelease()
			if err != nil {
				log.Printf("Error getting releases: %s\n",
					err.Error())
				// with `release` remaining as `nil`, allow the function to
				// fall through to its sleep
			}

			restartRequired := false
			if release != nil && lastReleaseID != release.ID {
				restartRequired = true
				lastReleaseID = release.ID
			}

			if restartRequired {
				log.Printf("New release %s detected", lastReleaseID)
				// This is a blocking channel and so restarts
				// will be throttled naturally.
				releaseChannel <- release
			} else {
				log.Printf("No new releases\n")
				<-time.After(10 * time.Second)
			}
		}
	}()

	return releaseChannel
}

func (p *Processes) addFormation(release *heroku.Release,
	argv []string, config map[string]string, concurrency int) *Formation {
	f := &Formation{
		release:     release,
		config:      config,
		concurrency: concurrency,
		argv:        argv,
	}
	p.formations = append(p.formations, f)
	return f
}

func (p *Processes) start(release *heroku.Release, command string,
	argv []string, concurrency int) {
	config, err := p.cl.ConfigVarInfo(p.app)
	if err != nil {
		log.Fatal("hsup could not get config info: " + err.Error())
	}

	slug, err := p.cl.SlugInfo(p.app, release.Slug.ID)
	if err != nil {
		log.Fatal("hsup could not get slug info: " + err.Error())
	}

	release2 := &Release{
		appName: p.app,
		config:  config,
		slugURL: slug.Blob.URL,
		version: release.Version,
	}
	err = p.dd.Build(release2)
	if err != nil {
		log.Fatal("hsup could not bake image for release " + release2.Name() + ": " + err.Error())
	}

	if command == "start" {
		var formations []*heroku.Formation
		if len(argv) == 0 {
			formations, err = p.cl.FormationList(p.app, &heroku.ListRange{})
			if err != nil {
				log.Fatal("hsup could not get formation list: " + err.Error())
			}
		} else {
			formation, err := p.cl.FormationInfo(p.app, argv[0])
			if err != nil {
				log.Fatal("hsup could not get formation list: " + err.Error())
			}
			formations = []*heroku.Formation{formation}
		}

		for _, formation := range formations {
			log.Printf("formation quantity=%v type=%v\n",
				formation.Quantity, formation.Type)
			f := p.addFormation(release, argv, config, concurrency)

			for i := 0; i < getConcurrency(concurrency, formation.Quantity); i++ {
				executor := &Executor{
					argv:        []string{formation.Command},
					dynoDriver:  p.dd,
					processID:   strconv.Itoa(i + 1),
					processType: formation.Type,
					release:     release2,
					complete:    make(chan struct{}),
					state:       Stopped,
					newInput:    make(chan DynoInput),
				}

				f.executors = append(f.executors, executor)
			}
		}
	} else if command == "run" {
		f := p.addFormation(release, argv, config, concurrency)

		for i := 0; i < getConcurrency(concurrency, 1); i++ {
			executor := &Executor{
				argv:        argv,
				dynoDriver:  p.dd,
				processID:   strconv.Itoa(i + 1),
				processType: "run",
				release:     release2,
				complete:    make(chan struct{}),
				state:       Stopped,
				OneShot:     true,
				newInput:    make(chan DynoInput),
			}
			f.executors = append(f.executors, executor)
		}
	}

	p.startParallel()
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

	p := Processes{
		app: *appName,
		cl:  cl,
		dd:  dynoDriver,
	}

	out := p.startReleasePoll()
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case release := <-out:
			p.stopParallel()
			p.start(release, args[0], args[1:], *concurrency)
		case sig := <-signals:
			log.Println("hsup caught a deadly signal:", sig)
			p.stopParallel()
			os.Exit(1)
		}
	}
}

func (p *Processes) startParallel() {
	for _, formation := range p.formations {
		for _, executor := range formation.executors {
			go func(executor *Executor) {
				go executor.Trigger(StayStarted)
				log.Println("Beginning Tickloop for", executor.Name())
				for executor.Tick() != ErrExecutorComplete {
				}
				log.Println("Executor completes", executor.Name())
			}(executor)
		}
	}
}

// Docker containers shut down slowly, so parallelize this operation
func (p *Processes) stopParallel() {
	log.Println("stopping everything")

	for _, formation := range p.formations {
		for _, executor := range formation.executors {
			go func(executor *Executor) {
				go executor.Trigger(Retire)
			}(executor)
		}
	}

	for _, formation := range p.formations {
		for _, executor := range formation.executors {
			<-executor.complete
		}
	}

}
