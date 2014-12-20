package main

import (
	"flag"
	"github.com/cyberdelia/heroku-go/v3"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func createCommand(config map[string]string, executable string,
	args []string) *exec.Cmd {
	cmd := exec.Command(executable, args...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Fill environment vector from Heroku configuration.
	for k, v := range config {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	return cmd
}

// Propagates deadly Unix signals to the containerized child process group.
func signalListener(p *os.Process) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	group, err := os.FindProcess(-1 * p.Pid)
	if err != nil {
		log.Fatal(err)
	}

	for {
		sig := <-signals
		group.Signal(sig)
	}
}

// Listens for new releases by periodically polling the Heroku
// API. When a new release is detected it is sent to the returned
// channel.
func startReleasePoll(client *heroku.Service, app string) (
	out <-chan *heroku.Release) {
	lastRelease := ""
	relc := make(chan *heroku.Release)
	go func() {
		for {
			releases, err := client.ReleaseList(
				app, &heroku.ListRange{Descending: true,
					Field: "version", Max: 1})
			if err != nil {
				log.Printf("Error getting releases: %s\n",
					err.Error())

				// Set an empty array so that we can fall
				// through to the sleep.
				releases = []*heroku.Release{}
			}

			restartRequired := false
			if len(releases) > 0 && lastRelease != releases[0].ID {
				restartRequired = true
				lastRelease = releases[0].ID
			}

			if restartRequired {
				log.Printf("New release %s detected",
					lastRelease)
				// This is a blocking channel and so restarts
				// will be throttled naturally.
				relc <- releases[0]
			} else {
				log.Printf("No new releases\n")
				<-time.After(10 * time.Second)
			}
		}
	}()

	return relc
}

func restart(app string, dd DynoDriver,
	release *heroku.Release, args []string, cl *heroku.Service) {
	config, err := cl.ConfigVarInfo(app)
	if err != nil {
		log.Fatal("hsup could not get config info: " + err.Error())
	}

	b := &Bundle{
		config:  config,
		release: release,
		argv:    args[1:],
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
	dynoDriver := flag.String("dynodriver", "simple",
		"specify a dynoDriver driver (program that starts a program)")
	flag.Parse()
	args := flag.Args()
	switch len(args) {
	case 0:
		log.Fatal("hsup requires an app name")
	case 1:
		log.Fatal("hsup requires an argument program")
	}

	dd, err := FindDynoDriver(*dynoDriver)
	if err != nil {
		log.Fatalln("could not find dyno driver:", *dynoDriver)
	}

	app := args[0]

	out := startReleasePoll(cl, app)
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case release := <-out:
			restart(app, dd, release, args, cl)
		case sig := <-signals:
			log.Println("hsup exits on account of signal:", sig)
			err = dd.Stop()
			if err != nil {
				log.Println("process stopped with error:", err)
			}
			os.Exit(1)
		}
	}

	os.Exit(0)
}
