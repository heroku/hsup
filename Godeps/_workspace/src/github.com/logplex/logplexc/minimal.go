package logplexc

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Running statistics on MiniClient operation
type MiniStats struct {
	NumberFramed uint64
	Buffered     int
}

// Configuration of a MiniClient.
type MiniConfig struct {
	// The configuration is by-value to prevent most kinds of accidental
	// sharing of modifications between clients.  Also, modification of
	// the URL is compulsary in the constructor, and it's not desirable to
	// modify the version passed by the user as a side effect of
	// constructing a client instance.
	Logplex    url.URL
	HttpClient http.Client
}

// A bundle of log messages.  Bundles are used to act as buffers of
// multiple log records as well as the unit of work for submittted to
// Logplex.
type Bundle struct {
	MiniStats
	outbox bytes.Buffer
}

// MiniClient implements a low-level, synchronous Logplex client.  It
// can format messages and gather statistics.  Most uses of logplexc
// are anticipated to use logplexc.Client.
type MiniClient struct {
	// Configuration that should not be mutated after creation
	MiniConfig

	// Cached copy of the token, extracted from the Logplex URL.
	token string

	reqInFlight sync.WaitGroup

	// Messages that have been collected but not yet sent.
	bSwapLock sync.Mutex
	b         *Bundle
}

func NewMiniClient(cfg *MiniConfig) (client *MiniClient, err error) {
	c := MiniClient{}

	c.b = &Bundle{outbox: bytes.Buffer{}}

	// Make a private copy
	c.MiniConfig = *cfg

	if c.MiniConfig.Logplex.User == nil {
		return nil, errors.New("No logplex user information provided")
	}

	token, ok := c.MiniConfig.Logplex.User.Password()
	if !ok {
		return nil, errors.New("No logplex password provided")
	}

	c.token = token

	return &c, nil
}

// Unsynchronized statistics gathering function
//
// Useful as a subroutine for procedures that already have taken care
// of synchronization.
func unsyncStats(b *Bundle) MiniStats {
	return b.MiniStats
}

// Copy the statistics structure embedded in the client.
func (c *MiniClient) Statistics() MiniStats {
	c.bSwapLock.Lock()
	defer c.bSwapLock.Unlock()

	return unsyncStats(c.b)
}

// Buffer a message for best-effort delivery to Logplex.
//
// Return the critical statistics on what has been buffered so far so
// that the caller can opt to PostMessages() and empty the buffer.
//
// No effort is expended to clean up bad bytes disallowed by syslog,
// as Logplex has a length-prefixed format.
func (c *MiniClient) BufferMessage(
	priority int, when time.Time, host string, procId string,
	log []byte) MiniStats {
	ts := when.UTC().Format(time.RFC3339)
	syslogPrefix := "<" + strconv.Itoa(priority) + ">1 " + ts + " " +
		host + " " + c.token + " " + procId + " - - "
	msgLen := len(syslogPrefix) + len(log)

	// Avoid racing against other operations that may want to swap
	// out client's current bundle.
	c.bSwapLock.Lock()
	defer c.bSwapLock.Unlock()

	fmt.Fprintf(&c.b.outbox, "%d %s%s", msgLen, syslogPrefix, log)
	c.b.NumberFramed += 1
	c.b.Buffered = c.b.outbox.Len()

	return unsyncStats(c.b)
}

// Swap out the bundle of logs for a fresh one, so that buffering can
// continue again immediately.  It's the caller's perogative to submit
// the returned, completed Bundle to logplex.
func (c *MiniClient) SwapBundle() Bundle {
	c.bSwapLock.Lock()
	defer c.bSwapLock.Unlock()

	var newB Bundle
	var oldB Bundle

	oldB = *c.b
	c.b = &newB

	return oldB
}

// Send a Bundle of logs to Logplex via HTTP POST.
func (c *MiniClient) Post(b *Bundle) (*http.Response, error) {
	// Record that a request is in progress so that a clean
	// shutdown can wait for it to complete.
	c.reqInFlight.Add(1)
	defer c.reqInFlight.Done()

	req, err := http.NewRequest("POST", c.Logplex.String(), &b.outbox)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/logplex-1")
	req.Header.Add("Logplex-Msg-Count",
		strconv.FormatUint(b.NumberFramed, 10))

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
