package main

import (
	"github.com/heroku/hk/hkclient"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

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
	var nrc *hkclient.NetRc
	var err error

	if nrc, err = hkclient.LoadNetRc(); err != nil {
		log.Fatal("envrun could not load netrc: " + err.Error())
	}

	cl, err := hkclient.New(nrc, "envrun")
	if err != nil {
		log.Fatal("envrun could not create client: " + err.Error())
	}

	app := os.Args[1]
	subArgs := os.Args[2:]

	config, err := cl.Client.ConfigVarInfo(app)
	if err != nil {
		log.Fatal("envrun could not get config info: " + err.Error())
	}

	cmd := exec.Command(subArgs[0], subArgs[1:]...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Fill environment vector from Heroku configuration.
	for k, v := range config {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Let $PATH leak into the environment started: otherwise
	// simple programs won't be available, much less complicated
	// $PATH mangling programs like "bundle" or "rbenv".
	cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH"))

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
