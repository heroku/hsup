package hsup

import (
	"io"
	"net/url"

	"io/ioutil"

	shuttle "github.com/heroku/log-shuttle"
)

type relay struct {
	cl   *shuttle.Shuttle
	name string
}

func newRelay(logplex *url.URL, name string) (*relay, error) {
	cfg := shuttle.NewConfig()
	cfg.LogsURL = logplex.String()
	cfg.Appname = "app"
	cfg.Procid = name
	cfg.ComputeHeader()

	cl := shuttle.NewShuttle(cfg)
	cl.Launch()

	return &relay{cl: cl, name: name}, nil
}

func (rl *relay) run(in io.Reader) {
	rl.cl.ReadLogLines(ioutil.NopCloser(in))
}

func (rl *relay) stop() {
	rl.cl.Land()
}

func teePipe(dst io.Writer) (io.Reader, io.Writer) {
	r, w := io.Pipe()
	tr := io.TeeReader(r, dst)
	return tr, w
}
