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

var executors []*Executor

func fetchLatestRelease(client *heroku.Service, app string) (*heroku.Release, error) {
	releases, err := client.ReleaseList(
		app, &heroku.ListRange{Descending: true, Field: "version", Max: 1})
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
func startReleasePoll(client *heroku.Service, app string) (
	out <-chan *heroku.Release) {
	lastReleaseID := ""
	releaseChannel := make(chan *heroku.Release)
	go func() {
		for {
			release, err := fetchLatestRelease(client, app)
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

func start(app string, dd DynoDriver,
	release *heroku.Release, command string, argv []string, cl *heroku.Service, concurrency int) {
	executors = nil

	config, err := cl.ConfigVarInfo(app)
	if err != nil {
		log.Fatal("hsup could not get config info: " + err.Error())
	}

	slug, err := cl.SlugInfo(app, release.Slug.ID)
	if err != nil {
		log.Fatal("hsup could not get slug info: " + err.Error())
	}

	release2 := &Release{
		appName: app,
		config:  config,
		slugUrl: slug.Blob.URL,
		version: release.Version,
	}
	err = dd.Build(release2)
	if err != nil {
		log.Fatal("hsup could not bake image for release " + release2.Name() + ": " + err.Error())
	}

	if command == "start" {
		var formations []*heroku.Formation
		if len(argv) == 0 {
			formations, err = cl.FormationList(app, &heroku.ListRange{})
			if err != nil {
				log.Fatal("hsup could not get formation list: " + err.Error())
			}
			formations = formations
		} else {
			formation, err := cl.FormationInfo(app, argv[0])
			if err != nil {
				log.Fatal("hsup could not get formation list: " + err.Error())
			}
			formations = []*heroku.Formation{formation}
		}

		for _, formation := range formations {
			log.Printf("formation quantity=%v type=%v\n",
				formation.Quantity, formation.Type)

			for i := 0; i < getConcurrency(concurrency, formation.Quantity); i++ {
				executor := &Executor{
					argv:        []string{formation.Command},
					dynoDriver:  dd,
					processID:   strconv.Itoa(i + 1),
					processType: formation.Type,
					release:     release2,
				}
				executors = append(executors, executor)
			}
		}
	} else if command == "run" {
		for i := 0; i < getConcurrency(concurrency, 1); i++ {
			executor := &Executor{
				argv:        argv,
				dynoDriver:  dd,
				processID:   strconv.Itoa(i + 1),
				processType: "run",
				release:     release2,
			}
			executors = append(executors, executor)
		}
	}

	startParallel()
}

func getConcurrency(concurrency int, defaultConcurrency int) int {
	if concurrency == -1 {
		return defaultConcurrency
	} else {
		return concurrency
	}
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

	out := startReleasePoll(cl, *appName)
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case release := <-out:
			stopParallel()
			start(*appName, dynoDriver, release, args[0], args[1:], cl, *concurrency)
		case sig := <-signals:
			log.Println("hsup caught a deadly signal:", sig)
			stopParallel()
			os.Exit(1)
		}
	}
}

func startParallel() {
	for _, executor := range executors {
		go executor.Start()
	}
}

// Docker containers shut down slowly, so parallelize this operation
func stopParallel() {
	if executors == nil {
		return
	}

	chans := make([]chan struct{}, len(executors))
	for i, executor := range executors {
		chans[i] = make(chan struct{})
		go func(executor *Executor, stopChan chan struct{}) {
			executor.Stop()
			stopChan <- struct{}{}
		}(executor, chans[i])
	}

	for _, stopChan := range chans {
		<-stopChan
	}
}
