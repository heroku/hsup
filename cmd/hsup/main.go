package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/cyberdelia/heroku-go/v3"
	"github.com/heroku/hsup"
	flag "github.com/ogier/pflag"
)

func statuses(p *hsup.Processes) <-chan []*hsup.ExitStatus {
	if p == nil || !p.OneShot {
		return nil
	}

	out := make(chan []*hsup.ExitStatus)

	go func() {
		statuses := make([]*hsup.ExitStatus, len(p.Executors))
		for i, executor := range p.Executors {
			log.Println("Got a status")
			statuses[i] = <-executor.Status
		}
		out <- statuses
	}()

	return out
}

func findDynoDriver(name string) (hsup.DynoDriver, error) {
	switch name {
	case "simple":
		return &hsup.SimpleDynoDriver{}, nil
	case "docker":
		return &hsup.DockerDynoDriver{}, nil
	case "abspath":
		return &hsup.AbsPathDynoDriver{}, nil
	case "libcontainer":
		newRoot := os.Getenv("HSUP_NEWROOT")
		if newRoot == "" {
			return nil, fmt.Errorf("HSUP_NEWROOT empty")
		}

		hostname := os.Getenv("HSUP_HOSTNAME")
		if hostname == "" {
			return nil, fmt.Errorf("HSUP_HOSTNAME empty")
		}

		user := os.Getenv("HSUP_USER")
		if user == "" {
			return nil, fmt.Errorf("HSUP_USER empty")
		}

		return &LibContainerDynoDriver{
			NewRoot:  newRoot,
			User:     user,
			Hostname: hostname,
		}, nil
	default:
		return nil, fmt.Errorf("could not locate driver. "+
			"specified by the user: %v", name)
	}
}

type ConcResolver interface {
	Resolve(hsup.Formation) int
}

type DefaultConcResolver struct{}

func (cr DefaultConcResolver) Resolve(form hsup.Formation) int {
	// By default, run only one of every process that has scale
	// factors above zero.
	if form.Quantity() > 0 {
		return 1
	}

	return 0
}

type ExplicitConcResolver map[string]int

func MustParseExplicitConcResolver(args []string) ExplicitConcResolver {
	// Parse a slice full of fragments like: "web=1", "worker=2"
	// and build a map from formation type names to scale factors
	// if there are no parsing errors.
	ret := make(map[string]int)
	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		switch len(parts) {
		case 2:
			n, err := strconv.Atoi(parts[1])
			if err != nil {
				log.Fatalln("could not parse parallelism " +
					"specification: not a valid integer.")
			}

			ret[parts[0]] = n
		case 1:
			ret[parts[0]] = 1
		default:
			panic(fmt.Sprintf("BUG: start parsing %v, %v", args,
				parts))
		}

	}
	return ret
}

func (cr ExplicitConcResolver) Resolve(form hsup.Formation) int {
	return cr[form.Type()]
}

func start(p *hsup.Processes, command string, args []string, concurrency int,
	startNumber int) (
	err error) {
	if os.Getenv("HSUP_SKIP_BUILD") != "TRUE" {
		err = p.Dd.Build(p.Rel)
	}

	if err != nil {
		log.Printf("hsup could not bake image for release %s: %s",
			p.Rel.Name(), err.Error())
		return err
	}

	switch command {
	case "start":
		var cr ConcResolver
		switch len(args) {
		case 0:
			cr = DefaultConcResolver{}
		default:
			cr = MustParseExplicitConcResolver(args)
		}

		for _, form := range p.Forms {
			conc := cr.Resolve(form)
			log.Printf("formation quantity=%v type=%v\n",
				conc, form.Type())
			for i := 0; i < conc; i++ {
				lpid := strconv.Itoa(i + startNumber)
				executor := &hsup.Executor{
					Args:        form.Args(),
					DynoDriver:  p.Dd,
					ProcessID:   lpid,
					ProcessType: form.Type(),
					Release:     p.Rel,
					Complete:    make(chan struct{}),
					State:       hsup.Stopped,
					OneShot:     p.OneShot,
					NewInput:    make(chan hsup.DynoInput),
				}

				if executor.OneShot {
					executor.Status = make(chan *hsup.ExitStatus)
				}

				p.Executors = append(p.Executors, executor)
			}
		}
	case "run":
		p.OneShot = true
		conc := getConcurrency(concurrency, 1)
		for i := 0; i < conc; i++ {
			lpid := strconv.Itoa(i + startNumber)
			executor := &hsup.Executor{
				Args:        args,
				DynoDriver:  p.Dd,
				ProcessID:   lpid,
				ProcessType: "run",
				Release:     p.Rel,
				Complete:    make(chan struct{}),
				State:       hsup.Stopped,
				OneShot:     true,
				Status:      make(chan *hsup.ExitStatus),
				NewInput:    make(chan hsup.DynoInput),
			}
			p.Executors = append(p.Executors, executor)
		}
	case "build":
		p.OneShot = true
	}

	startParallel(p)
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
	log.Println("Starting hsup")

	controlGob := os.Getenv("HSUP_CONTROL_GOB")
	token := os.Getenv("HEROKU_ACCESS_TOKEN")
	controlDir := os.Getenv("HSUP_CONTROL_DIR")

	if token == "" && controlDir == "" && controlGob == "" {
		// Omit mentioning "HSUP_CONTROL_GOB" as guidance to
		// avoid this error even if it is technically accurate
		// because it is only ever submitted by
		// self-invocations of hsup, i.e. that is invariably a
		// bug and not useful guidance for most humans.
		log.Fatal("need HEROKU_ACCESS_TOKEN or HSUP_CONTROL_DIR")
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

	log.Println("Args:", args, "LLArgs:", os.Args)
	irData := os.Getenv("HSUP_INITRETURN_DATA")
	if irData != "" {
		// Used only with libcontainer Exec to set up
		// namespaces and the like.  This *will* clear
		// environment variables and Args from
		// "CreateCommand", so be sure to be done processing
		// or storing them before executing.
		log.Println("running InitReturns")
		if err := mustInit(irData); err != nil {
			log.Fatal(err)
		}
	}

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

	dynoDriver, err := findDynoDriver(*dynoDriverName)
	if err != nil {
		log.Fatalln("could not initiate dyno driver:", err.Error())
	}

	// Inject information for delegation purposes to a
	// LibContainerDynoDriver.
	switch dd := dynoDriver.(type) {
	case *LibContainerDynoDriver:
		dd.envFill()
		dd.Args = args
		dd.AppName = *appName
		dd.Concurrency = *concurrency
	}

	var poller hsup.Notifier
	switch {
	case controlGob != "":
		poller = &hsup.GobNotifier{
			Dd:      dynoDriver,
			AppName: *appName,
			OneShot: *oneShot,
			Payload: controlGob,
		}
	case token != "":
		heroku.DefaultTransport.Username = ""
		heroku.DefaultTransport.Password = token
		cl := heroku.NewService(heroku.DefaultClient)
		poller = &hsup.APIPoller{
			Cl:      cl,
			App:     *appName,
			Dd:      dynoDriver,
			OneShot: *oneShot,
		}
	case controlDir != "":
		poller = &hsup.DirPoller{
			Dd:      dynoDriver,
			Dir:     controlDir,
			AppName: *appName,
			OneShot: *oneShot,
		}
	default:
		panic("one of token or watch dir ought to have been defined")
	}

	procs := poller.Notify()
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	var p *hsup.Processes

	if *controlPort != -1 {
		procs = hsup.StartControlAPI(*controlPort, procs)
	}

	for {
		select {
		case newProcs := <-procs:
			if p != nil {
				stopParallel(p)
			}
			p = newProcs
			err = start(p, args[0], args[1:], *concurrency,
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
				stopParallel(p)
			}
			os.Exit(1)
		}
	}
}

func startParallel(p *hsup.Processes) {
	for _, executor := range p.Executors {
		go func(executor *hsup.Executor) {
			go executor.Trigger(hsup.StayStarted)
			log.Println("Beginning Tickloop for", executor.Name())
			for executor.Tick() != hsup.ErrExecutorComplete {
			}
			log.Println("Executor completes", executor.Name())
		}(executor)
	}
}

// Docker containers shut down slowly, so parallelize this operation
func stopParallel(p *hsup.Processes) {
	log.Println("stopping everything")

	for _, executor := range p.Executors {
		go func(executor *hsup.Executor) {
			go executor.Trigger(hsup.Retire)
		}(executor)
	}

	for _, executor := range p.Executors {
		<-executor.Complete
	}
}
