package ftest

// Libcontainer driver functional tests

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func TestConfigurableLibcontainerDynoSubnet(t *testing.T) {
	onlyWithLibcontainer(t)

	output, err := run(
		AppMinimal, "", []string{
			"LIBCONTAINER_DYNO_SUBNET=192.168.200.0/30",
		},
		`ip -o addr show eth0 | grep -w inet | awk '{print $4}'`,
	)
	debug(t, output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.out.String()) != "192.168.200.2/30" {
		t.Fatal("Expected the assigned IP to be: 192.168.200.2/30")
	}
}

func TestExtraInterface(t *testing.T) {
	onlyWithLibcontainer(t)

	output, err := run(
		AppMinimal, "", []string{
			"LIBCONTAINER_DYNO_EXTRA_INTERFACE=eth0:192.168.201.5/24",
		},
		`ip -o addr show eth1 | grep -w inet | awk '{print $4}'`,
	)
	debug(t, output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.out.String()) != "192.168.201.5/24" {
		t.Fatal("Expected an extra (eth1) interface with IP assigned to be: 192.168.201.5/24")
	}
}

func TestConfigurableUIDRange(t *testing.T) {
	onlyWithLibcontainer(t)

	// [3000,10000) range
	uid := strconv.Itoa(rand.Intn(7000) + 3000)
	output, err := run(
		AppMinimal, "", []string{
			// Force a single UID
			"LIBCONTAINER_DYNO_UID_MIN=" + uid,
			"LIBCONTAINER_DYNO_UID_MAX=" + uid,
		},
		`id -u`,
	)
	debug(t, output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.out.String()) != uid {
		t.Fatal("Expected the assigned UID to be: ", uid)
	}
}
