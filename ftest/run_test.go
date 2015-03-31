package ftest

import (
	"strings"
	"testing"

	"github.com/heroku/hsup"
)

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
	output, err := run(app, "env")
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
	output, err := run(app, "echo $TESTENTRY")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.out.String()) != "vAlId" {
		t.Fatal("Expected ENV var not found: $TESTENTRY")
	}
}
