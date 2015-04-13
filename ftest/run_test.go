package ftest

import (
	"strings"
	"testing"
)

func TestEnv(t *testing.T) {
	if driver == "simple" {
		t.Log("Skipping ENV tests on the simple driver")
		return
	}

	output, err := run(AppMinimal, []string{}, "env")
	debug(t, output)
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
	output, err := run(AppWithEnv, []string{}, "echo $TESTENTRY")
	debug(t, output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.out.String()) != "vAlId" {
		t.Fatal("Expected ENV var not found: $TESTENTRY")
	}
}
