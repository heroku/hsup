package hsup

import (
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func linuxAmd64Path() string {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return os.Args[0]
	}

	return os.Args[0] + "-linux-amd64"
}

var ErrNoReleases = errors.New("No releases found")

type Poller interface {
	Poll() <-chan *Processes
}

type Processes struct {
	r     *Release
	forms []Formation

	dd        DynoDriver
	OneShot   bool
	Executors []*Executor
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

func (p *Processes) Start(command string, args []string, concurrency int,
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
					executor.Status = make(chan *ExitStatus)
				}

				p.Executors = append(p.Executors, executor)
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
				Status:      make(chan *ExitStatus),
				newInput:    make(chan DynoInput),
			}
			p.Executors = append(p.Executors, executor)
		}
	case "build":
		p.OneShot = true
	}

	p.StartParallel()
	return nil
}

func getConcurrency(concurrency int, defaultConcurrency int) int {
	if concurrency == -1 {
		return defaultConcurrency
	}

	return concurrency
}


func (p *Processes) StartParallel() {
	for _, executor := range p.Executors {
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
func (p *Processes) StopParallel() {
	log.Println("stopping everything")

	for _, executor := range p.Executors {
		go func(executor *Executor) {
			go executor.Trigger(Retire)

		}(executor)
	}

	for _, executor := range p.Executors {
		<-executor.complete
	}
}
