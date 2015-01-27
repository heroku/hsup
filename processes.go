package hsup

import (
	"errors"
	"io"
	"log"
	"os"
	"runtime"
	"strings"

	"bitbucket.org/kardianos/osext"
)

var ErrNoReleases = errors.New("No releases found")

type Notifier interface {
	Notify() <-chan *Processes
}

type Processes struct {
	Rel   *Release
	Forms []Formation

	Dd        DynoDriver
	OneShot   bool
	Executors []*Executor
}

type Formation interface {
	Args() []string
	Quantity() int
	Type() string
}

func linuxAmd64Path() string {
	exe, err := osext.Executable()
	if err != nil {
		log.Fatalln("could not locate own executable:", err)
	}

	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return exe
	}

	return exe + "-linux-amd64"
}

func copyFile(src, dst string, mode os.FileMode) (err error) {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		err = combine(err, r.Close())
	}()

	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		err = combine(err, w.Close())
	}()

	if _, err := io.Copy(w, r); err != nil {
		return err
	}
	if err := os.Chmod(dst, mode); err != nil {
		return err
	}
	return nil
}

type combinedError []error

// combine needs to return error, not a combinedError, otherwise nil errors
// aren't going to be treated as nil. More details:
// http://golang.org/doc/faq#nil_error
func combine(errors ...error) error {
	errors = make(combinedError, 0, len(errors))
	for _, err := range errors {
		if err == nil {
			continue
		}
		errors = append(errors, err)
	}
	if len(errors) == 0 {
		return nil // important so that callers can do if err == nil
	}
	return combinedError(errors)
}

// Error implements the error interface
func (e combinedError) Error() string {
	msgs := make([]string, len(e))
	for i, entry := range e {
		msgs[i] = entry.Error()
	}
	return strings.Join(msgs, " | ")
}

func (e combinedError) String() string {
	return e.Error()
}
