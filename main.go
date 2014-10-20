package main

import (
	"github.com/cyberdelia/heroku-go/v3"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func createCommand(config map[string]string, executable string, args []string) *exec.Cmd {
	cmd := exec.Command(executable, args...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Fill environment vector from Heroku configuration.
	for k, v := range config {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Let $PATH leak into the environment started: otherwise simple programs
	// won't be available, much less complicated $PATH mangling programs like
	// "bundle" or "rbenv".
	cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH"))

	return cmd
}

// Propagate signals to child.
func signaler(p *os.Process) {
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

// This is a pretty flawed system that will replace any args that are obviously
// env vars. It's designed mostly to handle something like:
//
//     bin/web --port $PORT
//
// Env vars that are contained within double-quoted strings and the like will
// need a little more work.
func replaceEnvVarArgs(config map[string]string, args []string) {
	for i, arg := range args {
		if strings.HasPrefix(arg, "$") {
			args[i] = config[arg[1:]]
		}
	}
}

func restarter(p *os.Process) {
	group, err := os.FindProcess(-1 * p.Pid)
	if err != nil {
		log.Fatal(err)
	}

	// Begin graceful shutdown via SIGTERM.
	group.Signal(syscall.SIGTERM)

	t := time.NewTicker(10 * time.Second)
	<-t.C
	t.Stop()

	// No more time.
	group.Signal(syscall.SIGKILL)
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

	app := os.Args[1]
	executable := os.Args[2]
	args := os.Args[2:]

	config, err := cl.ConfigVarInfo(app)
	if err != nil {
		log.Fatal("hsup could not get config info: " + err.Error())
	}

	replaceEnvVarArgs(config, args)
	cmd := createCommand(config, executable, args)

	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

	go signaler(cmd.Process)

	if err := cmd.Wait(); err != nil {
		// Non-portable: only works on Unix work-alikes.
		ee := err.(*exec.ExitError)
		os.Exit(ee.Sys().(syscall.WaitStatus).ExitStatus())
	}

	os.Exit(0)
}
