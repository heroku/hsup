// +build linux

package hsup

import (
	"bytes"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// avoid clashes with IPs used by AWS (e.g.: the internal DNS server on
// ec2-classic is 172.16.0.23).
func TestFirstAvailablePrivateNet(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-libcontainer-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	driver, err := NewLibContainerDynoDriver(workDir)
	if err != nil {
		t.Fatal(err)
	}
	minUID := 3000
	net, err := driver.privateNetForUID(minUID)
	if err != nil {
		t.Fatal(err)
	}
	expected := basePrivateIP
	if !bytes.Equal(net.IP, expected.IP) ||
		!bytes.Equal(net.Mask, expected.Mask) {
		t.Fatalf("the first available private network is not"+
			" 172.16.0.28/30. Got %#+v, Want %#+v", net, expected)
	}
}

// RFC1918: 172.16/12 private address space
func TestAllocatesNetworksInRFC1918Space(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-libcontainer-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	driver, err := NewLibContainerDynoDriver(workDir)
	if err != nil {
		t.Fatal(err)
	}

	minUID := 3000

	one, err := driver.privateNetForUID(minUID + 1)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, one, &net.IPNet{
		IP:   net.IPv4(172, 16, 0, 32).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	twentyThree, err := driver.privateNetForUID(minUID + 23)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, twentyThree, &net.IPNet{
		IP:   net.IPv4(172, 16, 0, 120).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	big, err := driver.privateNetForUID(minUID + 2036)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, big, &net.IPNet{
		IP:   net.IPv4(172, 16, 31, 236).To4(),
		Mask: net.CIDRMask(30, 32),
	})
}

func checkIPNet(t *testing.T, got, expected *net.IPNet) {
	if !bytes.Equal(got.IP, expected.IP) ||
		!bytes.Equal(got.Mask, expected.Mask) {
		t.Fatalf("Expected IP: %s. Got: %s.", expected, got)
	}
}

func TestFindsAvailableUIDs(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-libcontainer-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	driver, err := NewLibContainerDynoDriver(workDir)
	if err != nil {
		t.Fatal(err)
	}
	driver.minUID = 1
	driver.maxUID = 3

	// some uids are already allocated...
	if err := createUIDFile(workDir, 1); err != nil {
		t.Fatal(err)
	}
	if err := createUIDFile(workDir, 3); err != nil {
		t.Fatal(err)
	}

	// uid=2 is the only available
	uid, gid, err := driver.findFreeUIDGID()
	if err != nil {
		t.Fatal(err)
	}
	if uid != 2 {
		t.Fatalf("uid=2 was the only available and wasn't allocated. "+
			"Found %d", uid)
	}
	if gid != 2 {
		t.Fatalf("gid=2 was the only available and wasn't allocated. "+
			"Found %d", gid)
	}
	if !checkUIDFile(workDir, 2) {
		t.Fatal("a uid file to lock uid=2 wasn't created")
	}
}

func TestOnlyUsesFreeUIDs(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-libcontainer-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	driver, err := NewLibContainerDynoDriver(workDir)
	if err != nil {
		t.Fatal(err)
	}
	driver.minUID = 3000
	driver.maxUID = 3004

	// some uids are already allocated...
	if err := createUIDFile(workDir, 3002); err != nil {
		t.Fatal(err)
	}
	if err := createUIDFile(workDir, 3003); err != nil {
		t.Fatal(err)
	}

	first, _, err := driver.findFreeUIDGID()
	if err != nil {
		t.Fatal(err)
	}
	if !checkUIDFile(workDir, first) {
		t.Fatalf("a uid file to lock uid=%d wasn't created", first)
	}

	second, _, err := driver.findFreeUIDGID()
	if err != nil {
		t.Fatal(err)
	}
	if !checkUIDFile(workDir, second) {
		t.Fatalf("a uid file to lock uid=%d wasn't created", second)
	}
	if first == second {
		t.Fatalf("allocated uids should not be reused."+
			" Failed %d != %d", first, second)
	}

	third, _, err := driver.findFreeUIDGID()
	if err != nil {
		t.Fatal(err)
	}
	if !checkUIDFile(workDir, third) {
		t.Fatalf("a uid file to lock uid=%d wasn't created", third)
	}
	if first == third || second == third {
		t.Fatalf("allocated uids should not be reused."+
			" Failed %d != %d != %d", first, second, third)
	}
}

func createUIDFile(workDir string, uid int) error {
	f, err := os.Create(filepath.Join(workDir, "uids", strconv.Itoa(uid)))
	if err != nil {
		return err
	}
	return f.Close()
}

func checkUIDFile(workDir string, uid int) bool {
	f, err := os.Open(filepath.Join(workDir, "uids"))
	if err != nil {
		return false
	}
	if err := f.Close(); err != nil {
		return false
	}
	return true
}
