package hsup

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"net/url"
	"strings"
)

type Action int

const (
	Build Action = iota
	Start
	Run
)

// Startup is a serializable struct sufficient to perform
// sub-invocations of hsup.
type Startup struct {
	// App contains a representation of a single release of an
	// application to run.
	App AppSerializable

	// OneShot is true when hsup terminates after the supervised
	// program exits.
	OneShot bool

	// StartNumber is the first allocated ProcessID.  e.g. "2" in
	// the case of "web.2".
	StartNumber int

	// Action enumerates the high level action of this hsup,
	// e.g. "run", "start", "build".
	Action Action

	// Driver specifies the DynoDriver used to execute a program
	// under hsup.  If used for sub-invocations, it must be
	// registered via "gob.Register".
	Driver DynoDriver

	// SkipBuild is set to true tos kip skip the build step of
	// running a program.  This is useful when hsup is being
	// executed in the context of an already-prepared image
	// containing a program.
	SkipBuild bool

	// Formation name for "Start" action.
	FormName string

	// ControlSocket specifies the unix socket the hsup API listens on.
	// Set to "" when API support is disabled.
	ControlSocket string

	// For use with "run".
	Args []string

	// Binds enumerates paths bound from the host into a
	// container.
	Binds map[string]string
}

type AppSerializable struct {
	Version   int
	Name      string
	Env       map[string]string
	Slug      string
	Stack     string
	Processes []FormationSerializable

	// LogplexURL specifies where to forward the supervised
	// process Stdout and Stderr when non-empty.
	LogplexURL string `json:",omitempty"`
}

// Convenience function for parsing the stringy LogplexURL.  This is
// helpful because gob encoding of url.URL values is not supported.
// It's presumed that the URL-conformance of LogplexURL has already
// been verified.
func (as *AppSerializable) MustParseLogplexURL() *url.URL {
	if as.LogplexURL == "" {
		return nil
	}

	u, err := url.Parse(as.LogplexURL)
	if err != nil {
		panic(fmt.Sprintln("BUG could not parse url: ", err))
	}

	return u
}

type FormationSerializable struct {
	FArgs     []string `json:"Args"`
	FQuantity int      `json:"Quantity"`
	FType     string   `json:"Type"`
}

func (fs *FormationSerializable) Args() []string {
	return fs.FArgs
}
func (fs *FormationSerializable) Quantity() int {
	return fs.FQuantity
}

func (fs *FormationSerializable) Type() string {
	return fs.FType
}

func (hs *Startup) ToBase64Gob() string {
	buf := bytes.Buffer{}
	b64enc := base64.NewEncoder(base64.StdEncoding, &buf)
	enc := gob.NewEncoder(b64enc)
	err := enc.Encode(hs)
	b64enc.Close()
	if err != nil {
		panic("could not encode gob:" + err.Error())
	}

	return buf.String()
}

func (hs *Startup) FromBase64Gob(payload string) {
	d := gob.NewDecoder(base64.NewDecoder(base64.StdEncoding,
		strings.NewReader(payload)))
	if err := d.Decode(hs); err != nil {
		panic("could not decode gob:" + err.Error())
	}
}

func (hs *Startup) Procs() *Processes {
	procs := &Processes{
		Rel: &Release{
			appName: hs.App.Name,
			config:  hs.App.Env,
			slugURL: hs.App.Slug,
			stack:   hs.App.Stack,
			version: hs.App.Version,
		},
		Forms:      make([]Formation, len(hs.App.Processes)),
		Dd:         hs.Driver,
		OneShot:    hs.OneShot,
		LogplexURL: hs.App.MustParseLogplexURL(),
	}

	for i := range hs.App.Processes {
		procs.Forms[i] = &hs.App.Processes[i]
	}

	return procs
}

func init() {
	gob.Register(&AbsPathDynoDriver{})
	gob.Register(&LibContainerInitDriver{})
}
