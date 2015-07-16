package ftest

// Libcontainer driver functional tests

import (
	"math/rand"
	"os/exec"
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

func TestExtraInterfaceRoutes(t *testing.T) {
	onlyWithLibcontainer(t)

	if out, err := exec.Command(
		"sh", "-c", "ip link add dev dummy10 type dummy",
	).CombinedOutput(); err != nil {
		t.Log(string(out))
		t.Fatal(err)
	}
	if out, err := exec.Command(
		"sh", "-c", "ip link set up dev dummy10",
	).CombinedOutput(); err != nil {
		t.Log(string(out))
		t.Fatal(err)
	}
	defer func() {
		if out, err := exec.Command(
			"sh", "-c", "ip link del dev dummy10",
		).CombinedOutput(); err != nil {
			t.Log(string(out))
			t.Fatal(err)
		}
	}()

	output, err := run(
		AppMinimal, "", []string{
			"LIBCONTAINER_DYNO_EXTRA_INTERFACE=dummy10:192.168.201.5/24",
			"LIBCONTAINER_DYNO_EXTRA_ROUTES=1.2.3.0/24:192.168.201.1:eth1,4.3.0.0/16:192.168.201.1:eth1,7.7.7.0/24:default:eth0",
		},
		`default=$(ip -o route | grep default | awk '{ print $3 }'); ip -o route | grep "7.7.7.0/24 via $default" | awk '{ print $1 }'; ip -o route | grep eth1 | grep via | awk '{ print $1, $3 }' | sort`,
	)
	debug(t, output)
	if err != nil {
		t.Fatal(err)
	}
	want := "7.7.7.0/24\n1.2.3.0/24 192.168.201.1\n4.3.0.0/16 192.168.201.1"
	if got := strings.TrimSpace(output.out.String()); got != want {
		t.Fatalf("got routes %q, wanted %q", got, want)
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
