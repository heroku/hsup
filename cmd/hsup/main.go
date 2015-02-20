package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/cyberdelia/heroku-go/v3"
	"github.com/heroku/hsup"
	"github.com/heroku/hsup/diag"
	flag "github.com/ogier/pflag"
)

// CmdLogplexURL is non-nil when a logplex URL is specified on the
// command line.  This has priority over the Control Directory variant
// of the same setting.
var CmdLogplexURL *url.URL

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
		return hsup.NewLibContainerDynoDriver("/var/lib/hsup")
	case "libcontainer-init":
		return &hsup.LibContainerInitDriver{}, nil
	default:
		return nil, fmt.Errorf("could not locate driver. "+
			"specified by the user: %v", name)
	}
}

func dumpOnSignal() {
	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGUSR1)

	go func() {
		for {
			<-signals
			dump()
		}
	}()
}

func dump() {
	for _, r := range diag.Contents() {
		last := len(r) - 1
		if last == 0 {
			continue
		}

		// Insert terminating newlines of records for
		// consistency.  Terminating newlines are seen in
		// records emitted by the "log" package and are absent
		// from direct uses of "diag."
		os.Stderr.WriteString(r)
		if r[last] != '\n' {
			os.Stderr.WriteString("\n")
		}
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

func start(p *hsup.Processes, hs *hsup.Startup, args []string) (err error) {
	if !hs.SkipBuild {
		if err = p.Dd.Build(p.Rel); err != nil {
			log.Printf(
				"hsup could not bake image for release %s: %s",
				p.Rel.Name(), err.Error())
			return err
		}
	}

	switch hs.Action {
	case hsup.Start:
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
				executor := &hsup.Executor{
					Args:        form.Args(),
					DynoDriver:  p.Dd,
					ProcessID:   i + hs.StartNumber,
					ProcessType: form.Type(),
					Release:     p.Rel,
					Complete:    make(chan struct{}),
					State:       hsup.Stopped,
					OneShot:     p.OneShot,
					NewInput:    make(chan hsup.DynoInput),
					LogplexURL:  logplexDefault(p),
					Binds:       hs.Binds,
				}

				if executor.OneShot {
					executor.Status = make(chan *hsup.ExitStatus)
				}

				p.Executors = append(p.Executors, executor)
			}
		}
	case hsup.Run:
		p.OneShot = true
		executor := &hsup.Executor{
			Args:        args,
			DynoDriver:  p.Dd,
			ProcessID:   hs.StartNumber,
			ProcessType: "run",
			Release:     p.Rel,
			Complete:    make(chan struct{}),
			State:       hsup.Stopped,
			OneShot:     true,
			Status:      make(chan *hsup.ExitStatus),
			NewInput:    make(chan hsup.DynoInput),
			LogplexURL:  logplexDefault(p),
			Binds:       hs.Binds,
		}

		p.Executors = append(p.Executors, executor)
	case hsup.Build:
		p.OneShot = true
	}

	startParallel(p)
	return nil
}

func bindParse(bs string) map[string]string {
	out := make(map[string]string)
	parts := strings.SplitN(bs, ":", 2)
	switch len(parts) {
	case 2:
		out[parts[0]] = parts[1]
	case 1:
		out[parts[0]] = parts[0]
	default:
		panic(fmt.Sprintf("BUG: start parsing %v, %v", bs, parts))
	}

	return out
}

func fromOptions(dst *hsup.Startup) (args []string) {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s COMMAND [OPTIONS]\n", os.Args[0])
		flag.PrintDefaults()
	}
	appName := flag.StringP("app", "a", "", "app name")
	oneShot := flag.BoolP("oneshot", "", false, "run as one-shot processes: "+
		"no restarting")
	startNumber := flag.IntP("start-number", "", 1,
		"the first assigned number to process types, e.g. web.1")
	dynoDriverName := flag.StringP("dynodriver", "d", "simple",
		"specify a dyno driver (program that starts a program)")
	controlSocket := flag.StringP("control-socket", "", "",
		"start a control api service listening on a unix socket at the specified path")
	logplex := flag.String("logplex-url", "",
		"a logplex url to send child process output")
	bind := flag.String("bind", "",
		"host paths that are available within the container, "+
			"e.g. /tmp:/app/mytmp")
	flag.Parse()
	args = flag.Args()

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

		dst.Action = hsup.Run
	case "build":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "\"build\" accepts no arguments")
			os.Exit(1)
		}
		dst.Action = hsup.Build
	case "start":
		dst.Action = hsup.Start
	default:
		fmt.Fprintf(os.Stderr, "Command not found: %v\n", args[0])
		flag.Usage()
		os.Exit(1)
	}

	dynoDriver, err := findDynoDriver(*dynoDriverName)
	if err != nil {
		log.Fatalln("could not initiate dyno driver:", err.Error())
	}

	dst.Driver = dynoDriver
	dst.App.Name = *appName
	dst.OneShot = *oneShot
	dst.StartNumber = *startNumber
	dst.ControlSocket = *controlSocket

	if *logplex != "" {
		if CmdLogplexURL, err = url.Parse(*logplex); err != nil {
			log.Fatalln("invalid --logplex-url format:", err)
		}
	}

	if *bind != "" {
		dst.Binds = bindParse(*bind)
	}

	return args[1:]
}

func logplexDefault(p *hsup.Processes) *url.URL {
	if CmdLogplexURL == nil {
		return p.LogplexURL
	}

	return CmdLogplexURL
}

func main() {
	dumpOnSignal()
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

	var hs hsup.Startup

	var args []string
	if controlGob != "" {
		hs.FromBase64Gob(controlGob)
	} else {
		args = fromOptions(&hs)
	}

	var poller hsup.Notifier
	switch {
	case controlGob != "":
		poller = &hsup.GobNotifier{Payload: controlGob}
	case token != "":
		if hs.App.Name == "" {
			log.Fatal("specify --app")
		}

		heroku.DefaultTransport.Username = ""
		heroku.DefaultTransport.Password = token
		cl := heroku.NewService(heroku.DefaultClient)
		poller = &hsup.APIPoller{Cl: cl, Hs: &hs}
	case controlDir != "":
		poller = &hsup.DirPoller{Hs: &hs, Dir: controlDir}
	default:
		panic("one of token or watch dir ought to have been defined")
	}

	procs := poller.Notify()
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	var p *hsup.Processes

	if hs.ControlSocket != "" {
		procs = hsup.StartControlAPI(hs.ControlSocket, procs)
	}

	for {
		select {
		case newProcs := <-procs:
			if p != nil {
				stopParallel(p)
			}
			p = newProcs
			if err = start(p, &hs, args); err != nil {
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
			// TODO: capture the exit status from executors
			os.Exit(1)
		}
	}
}

func startParallel(p *hsup.Processes) {
	for _, executor := range p.Executors {
		go func(executor *hsup.Executor) {
			go executor.Trigger(hsup.StayStarted)
			diag.Log("Beginning Tickloop for", executor.Name())
			for executor.Tick() != hsup.ErrExecutorComplete {
			}
			diag.Log("Executor completes", executor.Name())
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
