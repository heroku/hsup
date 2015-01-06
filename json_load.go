package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sync"
)

type conf struct {
	dstNew        func() interface{}
	path          string
	accessProtect sync.RWMutex

	snapshot interface{}

	// To control semantics of first Poll(), which may load
	// "loaded" from a cold start.
	beyondFirstTime bool
}

// Return value for complex multiple-error cases, as there are code
// paths here where error reporting itself can have errors.  Since
// cases where this is thought to happen are signs that things have
// seriously gone wrong, be assiduous in reporting as much information
// as possible.
type multiError struct {
	error
	nested error
}

func newConf(dst func() interface{}, path string) *conf {
	return &conf{
		dstNew: dst,
		path:   path,
	}
}

func (c *conf) loadedPath() string {
	return path.Join(c.path, "loaded")
}

func (c *conf) newPath() string {
	return path.Join(c.path, "new")
}

func (c *conf) rejPath() string {
	return path.Join(c.path, "rejected")
}

func (c *conf) errPath() string {
	return path.Join(c.path, "last_error")
}

func (c *conf) Snapshot() interface{} {
	c.accessProtect.RLock()
	defer c.accessProtect.RUnlock()

	return c.snapshot
}

func (c *conf) protWrite(newSnap interface{}) {
	c.accessProtect.Lock()
	defer c.accessProtect.Unlock()

	c.snapshot = newSnap
}

func (c *conf) pollFirstTime() (newInfo bool, err error) {
	lp := c.loadedPath()

	contents, err := ioutil.ReadFile(lp)
	if err != nil {
		if os.IsNotExist(err) {
			// old loaded doesn't exist: that's okay; it's
			// just a fresh database.
			return false, nil
		}

		return false, err
	}

	newSnap := c.dstNew()
	err = json.Unmarshal(contents, newSnap)
	if err != nil {
		return false, err
	}

	c.protWrite(newSnap)

	return true, nil
}

func (c *conf) Poll() (newInfo bool, err error) {
	// Handle first execution on creation of the db instance.
	if !c.beyondFirstTime {
		newInfo, err = c.pollFirstTime()
		if err != nil {
			return false, err
		}

		c.beyondFirstTime = true
	}

	p := c.newPath()
	contents, err := ioutil.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			// This is the most common branch, where no
			// "new" file has been provided for loading.
			// Being that, silence the error.
			return newInfo, nil
		}

		// Had some problems reading an existing file.
		return newInfo, err
	}

	// Validate that the JSON is in the expected format.
	newSnap := c.dstNew()
	nonfatale := json.Unmarshal(contents, newSnap)
	if nonfatale != nil {
		// Nope, can't understand the passed JSON, reject it.
		if err := c.reject(p, nonfatale); err != nil {
			return newInfo, multiError{
				error:  err,
				nested: nonfatale,
			}
		}

		// Rejection went okay: that's not considered an error
		// for the caller, because it's likely the caller will
		// want to do something extreme in event of Poll()
		// errors, which otherwise tend to arise from serious
		// conditions preventing data base manipulation like
		// "out of disk".
		return newInfo, nil
	}

	// The new serve mapping was loaded successfully: before
	// installing it reflect its state in the data base first, so
	// a crash will yield the new state rather than the old one.
	if err := c.persistLoaded(contents); err != nil {
		return newInfo, err
	}

	// Remove last_error and reject file as the persistence has
	// succeeded.  As these files are somewhat advisory, don't
	// consider it a failure if such removals do not succeed.
	os.Remove(c.errPath())
	os.Remove(c.rejPath())

	// Commit to the new mappings in this session.
	c.protWrite(newSnap)

	return true, nil
}

// Persist the verified contents, which are presumed valid.
//
// This is done carefully through temporary files and renames for
// reasons of atomicity, and with both file and directory flushing for
// durability.
func (c *conf) persistLoaded(contents []byte) (err error) {
	// Get a file descriptor for the directory before doing
	// anything too complex, because it's necessary for this to
	// succeed before being able to process Sync() requests.
	dir, err := os.Open(c.path)
	if err != nil {
		return err
	}
	defer dir.Close()

	tempf, err := ioutil.TempFile(c.path, "tmp_")
	renamedOk := false
	if err != nil {
		return err
	}

	// Handle closing the temporary file and nesting errors.
	defer func() {
		// If no rename has taken place, unlink the temp file.
		if !renamedOk {
			if e := os.Remove(tempf.Name()); e != nil {
				if err != nil {
					err = multiError{
						error:  e,
						nested: err,
					}
				}
			}
		}

		// Close the temp file.
		if e := tempf.Close(); e != nil {
			if err != nil {
				err = multiError{error: e, nested: err}
			}
		}
	}()

	// Fill the temp file with the accepted contents
	_, err = tempf.Write(contents)
	if err != nil {
		return err
	}

	err = tempf.Sync()
	if err != nil {
		return err
	}

	// Move the temporary file into place
	err = os.Rename(tempf.Name(), c.loadedPath())
	if err != nil {
		return err
	}

	// Even though rename is not yet durable, it is visible
	// already.
	renamedOk = true

	// Flush the rename so a crash will not effectively un-do it.
	err = dir.Sync()
	if err != nil {
		return err
	}

	// Purge submitted serve file, as it has been accepted and
	// copied.
	err = os.Remove(c.newPath())
	if err != nil {
		return err
	}

	// Flush to make the removal of the submitted file durable.
	err = dir.Sync()
	if err != nil {
		return err
	}

	return nil
}

func (c *conf) reject(submitPath string, nonfatale error) (err error) {
	// Perform move to the rejection file
	err = os.Rename(submitPath, c.rejPath())
	if err != nil {
		return err
	}

	// Render and write the cause of the rejection.  Don't bother
	// syncing it to disk: an incomplete or empty file on a crash
	// seems acceptable for now.
	err = ioutil.WriteFile(
		c.errPath(),
		[]byte(fmt.Sprintf("%#v\n", nonfatale)),
		0400)
	if err != nil {
		return err
	}

	return nil
}
