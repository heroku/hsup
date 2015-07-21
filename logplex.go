package hsup

import (
	"io"
	"net/url"
	"sync"

	shuttle "github.com/heroku/log-shuttle"
)

type relay struct {
	cl     *shuttle.Shuttle
	name   string
	out    io.ReadCloser
	err    io.ReadCloser
	readWG sync.WaitGroup
}

func newRelay(
	logplex *url.URL, name string,
	out, err io.ReadCloser,
) (*relay, error) {
	cfg := shuttle.NewConfig()
	cfg.LogsURL = logplex.String()
	cfg.Appname = "app"
	cfg.Procid = name
	cfg.ComputeHeader()

	cl := shuttle.NewShuttle(cfg)
	cl.Launch()

	return &relay{cl: cl, name: name, out: out, err: err}, nil
}

func (rl *relay) run() {
	rl.readWG.Add(2)
	go func() {
		defer rl.readWG.Done()
		rl.cl.ReadLogLines(rl.out)
	}()
	go func() {
		defer rl.readWG.Done()
		rl.cl.ReadLogLines(rl.err)
	}()
}

func (rl *relay) stop() {
	rl.out.Close()
	rl.err.Close()
	rl.readWG.Wait()
	rl.cl.Land()
}

type teeReadCloser struct {
	io.Reader
	io.Closer
}

func teePipe(dst io.Writer) (io.ReadCloser, io.WriteCloser) {
	r, w := io.Pipe()
	tr := io.TeeReader(r, dst)
	return &teeReadCloser{Reader: tr, Closer: r}, w
}
