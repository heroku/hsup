// +build linux

package hsup

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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
