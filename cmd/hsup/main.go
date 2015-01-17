package main

import(
	. "github.com/fdr/hsup"
	"os/signal"
	"syscall"
	"os"
	"log"
	"fmt"

	"github.com/cyberdelia/heroku-go/v3"
	flag "github.com/ogier/pflag"
)



func main() {
	var err error
	log.Println("Starting hsup")

	token := os.Getenv("HEROKU_ACCESS_TOKEN")
	controlDir := os.Getenv("CONTROL_DIR")

	if token == "" && controlDir == "" {
		log.Fatal("need HEROKU_ACCESS_TOKEN or CONTROL_DIR")
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s COMMAND [OPTIONS]\n", os.Args[0])
		flag.PrintDefaults()
	}
	appName := flag.StringP("app", "a", "", "app name")
	oneShot := flag.BoolP("oneshot", "", false, "run as one-shot processes: "+
				"no restarting")
	startNumber := flag.IntP("start-number", "", 1,
		"the first assigned number to process types, e.g. web.1")
	concurrency := flag.IntP("concurrency", "c", -1,
		"concurrency number")
	dynoDriverName := flag.StringP("dynodriver", "d", "simple",
		"specify a dyno driver (program that starts a program)")
	controlPort := flag.IntP("controlport", "p", -1, "start a control service on 127.0.0.1 on this port")
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
	case "build":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "\"build\" accepts no arguments")
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
		log.Fatalln("could not initiate dyno driver:", err.Error())
	}

	var poller Poller
	switch {
	case token != "":
		heroku.DefaultTransport.Username = ""
		heroku.DefaultTransport.Password = token
		cl := heroku.NewService(heroku.DefaultClient)
		poller = &APIPoller{
			Cl:      cl,
			App:     *appName,
			Dd:      dynoDriver,
			OneShot: *oneShot,
		}
	case controlDir != "":
		poller = &DirPoller{
			Dd:      dynoDriver,
			Dir:     controlDir,
			AppName: *appName,
			OneShot: *oneShot,
		}
	default:
		panic("one of token or watch dir ought to have been defined")
	}

	procs := poller.Poll()
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	var p *Processes

	if *controlPort != -1 {
		procs = StartControlAPI(*controlPort, procs)
	}

	for {
		select {
		case newProcs := <-procs:
			if p != nil {
				p.StopParallel()
			}
			p = newProcs
			err = p.Start(args[0], args[1:], *concurrency,
				*startNumber)
			if err != nil {
				log.Fatalln("could not start process:", err)
			}
		case statv := <-statuses(p):
			exitVal := 0
		for i, s := range statv {
			eName := p.Executors[i].Name()
			if s.Err != nil {
				log.Printf("could not execute %s: %s",
					eName, s.Err.Error())
				if 255 > exitVal {
					exitVal = 255
				}
			} else {
				log.Println(eName, "exits with code:",
					s.Code)
				if s.Code > exitVal {
					exitVal = s.Code
				}
			}
			os.Exit(exitVal)
		}
			os.Exit(0)
		case sig := <-signals:
			log.Println("hsup caught a deadly signal:", sig)
			if p != nil {
				p.StopParallel()
			}
			os.Exit(1)
		}
	}
}

func statuses(p *Processes) <-chan []*ExitStatus {
	if p == nil || !p.OneShot {
		return nil
	}

	out := make(chan []*ExitStatus)

	go func() {
		statuses := make([]*ExitStatus, len(p.Executors))
		for i, executor := range p.Executors {
			log.Println("Got a status")
			statuses[i] = <-executor.Status
		}
		out <- statuses
	}()

	return out
}