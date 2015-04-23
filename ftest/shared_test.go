package ftest

import (
	"fmt"
	"os"
	"testing"

	"github.com/heroku/hsup"
)

func TestMain(m *testing.M) {
	if binary == "" {
		fmt.Fprintln(os.Stderr, "no hsup binary specified, skipping functional tests")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Fixtures
var (
	AppMinimal = hsup.AppSerializable{
		Version:   1,
		Name:      "test-app-123",
		Slug:      "https://s3.amazonaws.com/sclasen-herokuslugs/slug.tgz",
		Stack:     "cedar-14",
		Processes: make([]hsup.FormationSerializable, 0),
	}

	AppWithEnv = hsup.AppSerializable{
		Version: 1,
		Name:    "test-app-123",
		Env: map[string]string{
			"TESTENTRY": "vAlId",
			"PORT":      "5000",
		},
		Slug:      "https://s3.amazonaws.com/sclasen-herokuslugs/slug.tgz",
		Stack:     "cedar-14",
		Processes: make([]hsup.FormationSerializable, 0),
	}
)

// Helpers
func onlyWithLibcontainer(t *testing.T) {
	if driver != "libcontainer" {
		t.Skip("Skipping libcontainer specific test on driver ", driver)
	}
}

func debug(t *testing.T, output *output) {
	t.Log(output.out.String())
	t.Log(output.err.String())
}
