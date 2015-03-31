package ftest

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/heroku/hsup"
)

var (
	binary string
	driver string
)

func init() {
	flag.StringVar(&binary, "hsup", "", "dyno execution driver")
	flag.StringVar(&driver, "driver", "simple", "dyno execution driver")
	flag.Parse()
	if binary == "" {
		panic("Missing the hsup binary location (-hsup flag).")
	}
}

type output struct {
	out bytes.Buffer
	err bytes.Buffer
}

func run(app hsup.AppSerializable, args ...string) (*output, error) {
	controlDir, err := ioutil.TempDir("", "hsup-test-control")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(controlDir)

	controlFile, err := os.Create(filepath.Join(controlDir, "new"))
	if err != nil {
		return nil, err
	}
	defer controlFile.Close()
	if err := json.NewEncoder(controlFile).Encode(&app); err != nil {
		return nil, err
	}
	if err := controlFile.Sync(); err != nil {
		return nil, err
	}

	cmd := exec.Command(binary, append(
		[]string{"-d", driver, "run"},
		args...,
	)...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HSUP_CONTROL_DIR=" + controlDir,
	}
	var output output
	cmd.Stdout = &output.out
	cmd.Stderr = &output.err
	return &output, cmd.Run()
}
