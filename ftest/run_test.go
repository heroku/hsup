package ftest

import (
	"fmt"
	"os"
	"strings"
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

func TestEnv(t *testing.T) {
	if driver == "simple" {
		t.Log("Skipping ENV tests on the simple driver")
		return
	}

	app := hsup.AppSerializable{
		Version:   1,
		Name:      "test-app-123",
		Slug:      "https://s3.amazonaws.com/sclasen-herokuslugs/slug.tgz",
		Stack:     "cedar-14",
		Processes: make([]hsup.FormationSerializable, 0),
	}
	output, err := run(app, []string{}, "env")
	t.Log(output.out.String())
	t.Log(output.err.String())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.out.String(), "DYNO=run.1") {
		t.Fatal("Expected ENV entry not found: DYNO")
	}
	if !strings.Contains(
		output.out.String(),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	) {
		t.Fatal("Expected ENV entry not found: DYNO")
	}
	if !strings.Contains(output.out.String(), "PWD=/app") {
		t.Fatal("Expected ENV entry not found: PWD")
	}
	if !strings.Contains(output.out.String(), "HOME=/app") {
		t.Fatal("Expected ENV entry not found: HOME")
	}
}

func TestSimpleBashExprWithVar(t *testing.T) {
	app := hsup.AppSerializable{
		Version: 1,
		Name:    "test-app-123",
		Env: map[string]string{
			"TESTENTRY": "vAlId",
		},
		Slug:      "https://s3.amazonaws.com/sclasen-herokuslugs/slug.tgz",
		Stack:     "cedar-14",
		Processes: make([]hsup.FormationSerializable, 0),
	}
	output, err := run(app, []string{}, "echo $TESTENTRY")
	t.Log(output.out.String())
	t.Log(output.err.String())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.out.String()) != "vAlId" {
		t.Fatal("Expected ENV var not found: $TESTENTRY")
	}
}

func TestConfigurableLibcontainerDynoSubnet(t *testing.T) {
	if driver != "libcontainer" {
		t.Log("Skipping libcontainer specific test on driver ", driver)
		return
	}

	app := hsup.AppSerializable{
		Version: 1,
		Name:    "test-app-123",
		Env: map[string]string{
			"TESTENTRY": "vAlId",
		},
		Slug:      "https://s3.amazonaws.com/sclasen-herokuslugs/slug.tgz",
		Stack:     "cedar-14",
		Processes: make([]hsup.FormationSerializable, 0),
	}
	output, err := run(
		app, []string{
			"LIBCONTAINER_DYNO_SUBNET=192.168.200.0/30",
		},
		`ip -o addr show eth0 | grep -w inet | awk '{print $4}'`,
	)
	t.Log(output.out.String())
	t.Log(output.err.String())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.out.String()) != "192.168.200.2/30" {
		t.Fatal("Expected the assigned IP to be: 192.168.200.2/30")
	}
}
