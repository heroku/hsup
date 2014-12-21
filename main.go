package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cyberdelia/heroku-go/v3"
)

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
	release *heroku.Release, args []string, processTypeName *string, cl *heroku.Service) {
	config, err := cl.ConfigVarInfo(app)
	if err != nil {
		log.Fatal("hsup could not get config info: " + err.Error())
	}

	slug, err := cl.SlugInfo(app, release.Slug.ID)
	if err != nil {
		log.Fatal("hsup could not get slug info: " + err.Error())
	}

	var formation *heroku.Formation
	if *processTypeName != "" {
		formation, err = cl.FormationInfo(app, *processTypeName)
		if err != nil {
			log.Fatal("hsup could not get formation list: " + err.Error())
		}
	}

	b := &Bundle{
		app:        app,
		argv:       args[1:],
		config:     config,
		formation:  formation,
		release:    release,
		slug:       slug,
	}

again:
	s := dd.State()
	switch s {
	case NeverStarted:
		fallthrough
	case Stopped:
		log.Println("starting")
		err = dd.Start(b)
		if err != nil {
			log.Println(
				"process could not start with error:",
				err)
		}
		log.Println("started")
	case Started:
		log.Println("Attempting to stop...")
		err = dd.Stop()
		if err != nil {
			log.Println("process stopped with error:", err)
		}
		log.Println("...stopped")
		goto again
	default:
		log.Fatalln("BUG bad state:", s)
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
	dynoDriverName := flag.String("dynodriver", "simple",
		"specify a dynoDriver driver (program that starts a program)")
	processTypeName := flag.String("type", "",
		"specify the type of process to start")
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		log.Fatal("hsup requires an app name")
	} else if len(args) == 1 && *processTypeName == "" {
		log.Fatal("hsup requires a process type or argument program")
	}

	dynoDriver, err := FindDynoDriver(*dynoDriverName)
	if err != nil {
		log.Fatalln("could not find dyno driver:", *dynoDriverName)
	}

	app := args[0]

	out := startReleasePoll(cl, app)
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case release := <-out:
			start(app, dynoDriver, release, args, processTypeName, cl)
		case sig := <-signals:
			log.Println("hsup caught a deadly signal:", sig)
			err = dynoDriver.Stop()
			if err != nil {
				log.Println("process stopped with error:", err)
			}
			os.Exit(1)
		}
	}

	os.Exit(0)
}
