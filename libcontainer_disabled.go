// +build !linux

package hsup

import "errors"

var ErrDriverNotSupported = errors.New(
	"the libcontainer driver is not supported on this platform",
)

type LibContainerDynoDriver struct{}

func NewLibContainerDynoDriver(string) (*LibContainerDynoDriver, error) {
	return nil, ErrDriverNotSupported
}
func (dd *LibContainerDynoDriver) Build(*Release) error {
	return ErrDriverNotSupported
}

func (dd *LibContainerDynoDriver) Start(*Executor) error {
	return ErrDriverNotSupported
}

func (dd *LibContainerDynoDriver) Stop(*Executor) error {
	return ErrDriverNotSupported
}

func (dd *LibContainerDynoDriver) Wait(*Executor) *ExitStatus {
	return nil
}

type LibContainerInitDriver struct{}

func (dd *LibContainerInitDriver) Build(*Release) error {
	return ErrDriverNotSupported
}

func (dd *LibContainerInitDriver) Start(*Executor) error {
	return ErrDriverNotSupported
}

func (dd *LibContainerInitDriver) Stop(*Executor) error {
	return ErrDriverNotSupported
}

func (dd *LibContainerInitDriver) Wait(*Executor) *ExitStatus {
	return nil
}
