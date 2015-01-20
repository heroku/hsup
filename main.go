package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/cyberdelia/heroku-go/v3"
	flag "github.com/ogier/pflag"
)

func linuxAmd64Path() string {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return os.Args[0]
	}

	return os.Args[0] + "-linux-amd64"
}

var ErrNoReleases = errors.New("No releases found")

type Notifier interface {
	Notify() <-chan *Processes
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

type ConcResolver interface {
	Resolve(Formation) int
}

type DefaultConcResolver struct{}

func (cr DefaultConcResolver) Resolve(form Formation) int {
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

func (cr ExplicitConcResolver) Resolve(form Formation) int {
	return cr[form.Type()]
}

func (p *Processes) start(command string, args []string, concurrency int,
	startNumber int) (
	err error) {
	if os.Getenv("HSUP_SKIP_BUILD") != "TRUE" {
		err = p.dd.Build(p.r)
	}

	if err != nil {
		log.Printf("hsup could not bake image for release %s: %s",
			p.r.Name(), err.Error())
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

		for _, form := range p.forms {
			conc := cr.Resolve(form)
			log.Printf("formation quantity=%v type=%v\n",
				conc, form.Type())
			for i := 0; i < conc; i++ {
				lpid := strconv.Itoa(i + startNumber)
				executor := &Executor{
					args:        form.Args(),
					dynoDriver:  p.dd,
					processID:   lpid,
					processType: form.Type(),
					release:     p.r,
					complete:    make(chan struct{}),
					state:       Stopped,
					OneShot:     p.OneShot,
					newInput:    make(chan DynoInput),
				}

				if executor.OneShot {
					executor.status = make(chan *ExitStatus)
				}

				p.executors = append(p.executors, executor)
			}
		}
	case "run":
		p.OneShot = true
		conc := getConcurrency(concurrency, 1)
		for i := 0; i < conc; i++ {
			lpid := strconv.Itoa(i + startNumber)
			executor := &Executor{
				args:        args,
				dynoDriver:  p.dd,
				processID:   lpid,
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
	case "build":
		p.OneShot = true
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

	var poller Notifier
	switch {
	case controlGob != "":
		poller = &GobNotifier{
			Dd:      dynoDriver,
			AppName: *appName,
			OneShot: *oneShot,
			Payload: controlGob,
		}
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

	procs := poller.Notify()
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
				p.stopParallel()
			}
			p = newProcs
			err = p.start(args[0], args[1:], *concurrency,
				*startNumber)
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
