package hsup

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"
)

type ControlDirFixture struct {
	json []byte
	repr string
}

var defaultFixture = ControlDirFixture{
	json: []byte(`{
    "Version": 1,
    "Env": {
        "NAME": "CONTENTS"
    },
    "Slug": "sample-slug.tgz",
    "Stack": "cedar-14",
    "Processes": [
        {
            "Args": ["./web-server", "arg"],
	    "Quantity": 2,
	    "Type": "web"
        },
        {
            "Args": ["./worker", "arg"],
	    "Quantity": 2,
	    "Type": "worker"
        }
    ]
}
`),
	repr: `{Version:1 Name: Env:map[NAME:CONTENTS] Slug:sample-slug.tgz Stack:cedar-14 Processes:[{FArgs:[./web-server arg] FQuantity:2 FType:web} {FArgs:[./worker arg] FQuantity:2 FType:worker}] LogplexURL:}`,
}

var anotherFixture = ControlDirFixture{
	json: []byte(`{
    "Version": 2,
    "Env": {
        "another": "fixture"
    },
    "Slug": "another-slug.tgz",
    "Stack": "cedar",
    "Processes": [
        {
            "Args": ["another", "fixture"],
	    "Quantity": 3,
	    "Type": "another-fixture"
        }
    ]
}
`),
	repr: `{Version:2 Name: Env:map[another:fixture] Slug:another-slug.tgz Stack:cedar Processes:[{FArgs:[another fixture] FQuantity:3 FType:another-fixture}] LogplexURL:}`,
}

func newTmpDb(t *testing.T) string {
	name, err := ioutil.TempDir("", "test_")
	if err != nil {
		t.Fatalf("Could not create temporary directory for test: %v",
			err)
	}

	return name
}

func TestEmptyDB(t *testing.T) {
	name := newTmpDb(t)
	defer os.RemoveAll(name)

	c := newConf(newControlDir, name)
	updates, err := c.Notify()

	if err != nil {
		t.Fatalf("Poll on an empty directory should succeed, "+
			"instead failed: %v", err)
	}

	if updates {
		t.Fatal("Expect no updates for first poll in an empty database")
	}

	updates, err = c.Notify()
	if err != nil {
		t.Fatalf("Poll on an empty directory should succeed, "+
			"instead failed: %v", err)
	}

	if updates {
		t.Fatal("Expect no updates for second poll")
	}
}

func TestColdStartLoaded(t *testing.T) {
	name := newTmpDb(t)
	defer os.RemoveAll(name)

	c := newConf(newControlDir, name)
	ioutil.WriteFile(c.loadedPath(), []byte(defaultFixture.json), 0400)

	// Expect a successful cold-start load.
	update, err := c.Notify()
	if err != nil {
		t.Fatal(err)
	}

	if !update {
		t.FailNow()
	}

	// Check Unmarshal contents.
	db := *c.Snapshot().(*AppSerializable)
	result := fmt.Sprintf("%+v", db)
	if result != defaultFixture.repr {
		t.Fatalf("\nExpect %v\nResult %v", defaultFixture.repr, result)
	}
}

func TestColdStartNew(t *testing.T) {
	name := newTmpDb(t)
	defer os.RemoveAll(name)

	c := newConf(newControlDir, name)
	ioutil.WriteFile(c.newPath(), []byte(defaultFixture.json), 0400)

	// Expect a successful cold-start load.
	update, err := c.Notify()
	if err != nil {
		t.Fatal(err)
	}

	if !update {
		t.Fatal("Expected update on cold-start")
	}

	// Verify promotion of "new" to "loaded"
	if _, err := os.Stat(c.loadedPath()); os.IsNotExist(err) {
		t.FailNow()
	}

	if _, err := os.Stat(c.newPath()); !os.IsNotExist(err) {
		t.FailNow()
	}

	// Check Unmarshal contents.
	db := *c.Snapshot().(*AppSerializable)
	if fmt.Sprintf("%+v", db) != defaultFixture.repr {
		t.FailNow()
	}
}

func TestLoadCycle(t *testing.T) {
	name := newTmpDb(t)
	defer os.RemoveAll(name)

	c := newConf(newControlDir, name)
	ioutil.WriteFile(c.newPath(), []byte(defaultFixture.json), 0400)

	// Expect a successful cold-start load.
	update, err := c.Notify()
	if err != nil {
		t.Fatal(err)
	}

	if !update {
		t.Fatal("Expected update on cold-start")
	}

	// Verify promotion of "new" to "loaded"
	if _, err := os.Stat(c.loadedPath()); os.IsNotExist(err) {
		t.FailNow()
	}

	if _, err := os.Stat(c.newPath()); !os.IsNotExist(err) {
		t.FailNow()
	}

	// Check Unmarshal contents.
	db := *c.Snapshot().(*AppSerializable)
	result := fmt.Sprintf("%+v", db)
	if result != defaultFixture.repr {
		t.FailNow()
	}

	// Check no-op load.
	update, err = c.Notify()
	if err != nil {
		t.Fatal("Expect no error from no-op load")
	}

	if update {
		t.Fatal("Expect no update from no-op load")
	}

	// Change configuration.
	ioutil.WriteFile(c.newPath(), anotherFixture.json, 0400)

	update, err = c.Notify()
	if err != nil {
		t.FailNow()
	}

	if !update {
		t.Fatal("Expect update since 'new' file updated")
	}

	db = *c.Snapshot().(*AppSerializable)
	result = fmt.Sprintf("%+v", db)
	if result != anotherFixture.repr {
		t.Fatal(result)
	}

	// Test errornous load.
	ioutil.WriteFile(c.newPath(), []byte(`bogus json`), 0400)

	update, err = c.Notify()
	if err != nil {
		t.Fatal("Invalid input is not cause for an error, " +
			"rather, the 'new' file is moved to 'reject'" +
			"the last_error file is filled.")
	}

	if _, err := os.Stat(c.rejPath()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(c.errPath()); err != nil {
		t.FailNow()
	}
}
