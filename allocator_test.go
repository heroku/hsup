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

// by default avoid clashes with IPs used by AWS (e.g.: the internal DNS server
// on ec2-classic is 172.16.0.23).
func TestFirstAvailableInDefaultPrivateNet(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-allocator-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	allocator, err := NewAllocator(workDir, DefaultPrivateSubnet)
	if err != nil {
		t.Fatal(err)
	}
	minUID := 3000
	first, err := allocator.privateNetForUID(minUID)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, first, &net.IPNet{
		IP:   net.IPv4(172, 16, 0, 28).To4(),
		Mask: net.CIDRMask(30, 32),
	})
}

// RFC1918: 172.16/12 private address space is the default
func TestAllocatesNetworksInRFC1918SpaceByDefault(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-allocator-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	allocator, err := NewAllocator(workDir, DefaultPrivateSubnet)
	if err != nil {
		t.Fatal(err)
	}

	minUID := 3000
	// the /12 block provides 2 ** 18 = 262144 /30 subnets, but the first 8
	// are not being used (skipped) by default
	maxUID := minUID + 262144 - 1 - 7

	one, err := allocator.privateNetForUID(minUID + 1)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, one, &net.IPNet{
		IP:   net.IPv4(172, 16, 0, 32).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	twentyThree, err := allocator.privateNetForUID(minUID + 23)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, twentyThree, &net.IPNet{
		IP:   net.IPv4(172, 16, 0, 120).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	big, err := allocator.privateNetForUID(minUID + 2036)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, big, &net.IPNet{
		IP:   net.IPv4(172, 16, 31, 236).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	last, err := allocator.privateNetForUID(maxUID)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, last, &net.IPNet{
		IP:   net.IPv4(172, 31, 255, 252).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	// out of the available range will wrap
	tooBig, err := allocator.privateNetForUID(maxUID + 1)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, tooBig, &net.IPNet{
		IP:   net.IPv4(172, 16, 0, 28).To4(),
		Mask: net.CIDRMask(30, 32),
	})
}

func TestAllocatesNetworksFromConfigurableBlock(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-allocator-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	// /16 block provides maximum 2**14 (16384) /30 subnets
	block := net.IPNet{
		IP:   net.IPv4(127, 128, 0, 0).To4(),
		Mask: net.CIDRMask(16, 32),
	}
	allocator, err := NewAllocator(workDir, block)
	if err != nil {
		t.Fatal(err)
	}

	minUID := 3000
	maxUID := minUID + 16384 - 1
	first, err := allocator.privateNetForUID(minUID)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, first, &net.IPNet{
		IP:   net.IPv4(127, 128, 0, 0).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	second, err := allocator.privateNetForUID(minUID + 1)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, second, &net.IPNet{
		IP:   net.IPv4(127, 128, 0, 4).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	big, err := allocator.privateNetForUID(minUID + 2036)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, big, &net.IPNet{
		IP:   net.IPv4(127, 128, 31, 208).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	last, err := allocator.privateNetForUID(maxUID)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, last, &net.IPNet{
		IP:   net.IPv4(127, 128, 255, 252).To4(),
		Mask: net.CIDRMask(30, 32),
	})

	// out of the available range will wrap
	tooBig, err := allocator.privateNetForUID(maxUID + 1)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, tooBig, &net.IPNet{
		IP:   net.IPv4(127, 128, 0, 0).To4(),
		Mask: net.CIDRMask(30, 32),
	})
}

// static IPs can be configured with a block that only provides one /30 subnet
func TestAllowsStaticIP(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-allocator-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	// smallest possible block, only provides 1 /30 subnet
	block := net.IPNet{
		IP:   net.IPv4(127, 128, 0, 0).To4(),
		Mask: net.CIDRMask(30, 32),
	}
	allocator, err := NewAllocator(workDir, block)
	if err != nil {
		t.Fatal(err)
	}

	for uid := 3000; uid <= 60000; uid++ {
		subnet, err := allocator.privateNetForUID(uid)
		if err != nil {
			t.Fatal(err)
		}
		checkIPNet(t, subnet, &net.IPNet{
			IP:   net.IPv4(127, 128, 0, 0).To4(),
			Mask: net.CIDRMask(30, 32),
		})
	}
}

func checkIPNet(t *testing.T, got, expected *net.IPNet) {
	if !bytes.Equal(got.IP, expected.IP) ||
		!bytes.Equal(got.Mask, expected.Mask) {
		t.Fatalf("Expected IP: %s. Got: %s.", expected, got)
	}
}

func TestFindsAvailableUIDs(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-allocator-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	allocator, err := NewAllocator(workDir, DefaultPrivateSubnet)
	if err != nil {
		t.Fatal(err)
	}
	allocator.minUID = 1
	allocator.maxUID = 3

	// some uids are already allocated...
	if err := createUIDFile(workDir, 1); err != nil {
		t.Fatal(err)
	}
	if err := createUIDFile(workDir, 3); err != nil {
		t.Fatal(err)
	}

	// uid=2 is the only available
	uid, err := allocator.ReserveUID()
	if err != nil {
		t.Fatal(err)
	}
	if uid != 2 {
		t.Fatalf("uid=2 was the only available and wasn't allocated. "+
			"Found %d", uid)
	}
	if !checkUIDFile(workDir, 2) {
		t.Fatal("a uid file to lock uid=2 wasn't created")
	}
}

func TestOnlyUsesFreeUIDs(t *testing.T) {
	workDir, err := ioutil.TempDir("", "hsup-allocator-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)
	allocator, err := NewAllocator(workDir, DefaultPrivateSubnet)
	if err != nil {
		t.Fatal(err)
	}
	allocator.minUID = 3000
	allocator.maxUID = 3004

	// some uids are already allocated...
	if err := createUIDFile(workDir, 3002); err != nil {
		t.Fatal(err)
	}
	if err := createUIDFile(workDir, 3003); err != nil {
		t.Fatal(err)
	}

	first, err := allocator.ReserveUID()
	if err != nil {
		t.Fatal(err)
	}
	if !checkUIDFile(workDir, first) {
		t.Fatalf("a uid file to lock uid=%d wasn't created", first)
	}

	second, err := allocator.ReserveUID()
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

	third, err := allocator.ReserveUID()
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
