package hsup

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/logplex/logplexc"
)

type relay struct {
	cl   *logplexc.Client
	name string
}

func newRelay(logplex *url.URL, name string) (*relay, error) {
	cfg := logplexc.Config{
		Logplex:            *logplex,
		HttpClient:         *http.DefaultClient,
		RequestSizeTrigger: 100 * 1024,
		Concurrency:        3,
		Period:             250 * time.Millisecond,
	}

	cl, err := logplexc.NewClient(&cfg)
	if err != nil {
		return nil, fmt.Errorf("could not set up log channel: %v", err)
	}

	return &relay{cl: cl, name: name}, nil
}

func (rl *relay) run(in io.Reader) {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		rl.cl.BufferMessage(134, time.Now(), "app", rl.name,
			[]byte(scanner.Text()))
	}
}

func teePipe(dst io.Writer) (io.Reader, io.Writer) {
	r, w := io.Pipe()
	tr := io.TeeReader(r, dst)
	return tr, w
}
