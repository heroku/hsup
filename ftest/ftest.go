package ftest

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"code.google.com/p/go-uuid/uuid"
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
}

type output struct {
	out bytes.Buffer
	err bytes.Buffer
}

func run(app hsup.AppSerializable, socket string, hsupEnv []string, args ...string) (*output, error) {
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

	cmdArgs := []string{"-d", driver}
	if socket != "" {
		cmdArgs = append(cmdArgs, "-s", socket)
	}
	cmdArgs = append(cmdArgs, "run")
	cmd := exec.Command(binary, append(cmdArgs, args...)...)
	cmd.Env = append(hsupEnv,
		"PATH="+os.Getenv("PATH"),
		"HSUP_CONTROL_DIR="+controlDir,
	)
	var output output
	cmd.Stdout = &output.out
	cmd.Stderr = &output.err
	return &output, cmd.Run()
}

func retryUntil(retries int, delay time.Duration, fn func() (bool, error)) (bool, error) {
	var success bool
	var err error

	for i := 0; i < retries; i++ {
		if i > 0 {
			time.Sleep(delay)
		}

		success, err = fn()
		if success {
			return true, nil
		}
	}

	return success, err
}

func newSocketFile() string {
	base := filepath.Base("/")
	socket := uuid.New() + ".sock"
	return filepath.Join(base, "etc", "hsup", "containers", "sockets", socket)
}
