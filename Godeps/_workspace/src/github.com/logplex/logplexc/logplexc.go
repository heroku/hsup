/*
Package logplexc provides Logplex clients that can be linked into
other Go programs.

Most users will want to use logplexc.Client, which implements a
concurrent client, including periodic flushing, dropping, statistics
accumulation, and request-level parallelism.  logplexc.Client is built
with logplexc.MiniClient.

To use the client, call NewClient with a configuration structure:

	cfg := logplexc.Config{
		Logplex:	    "https://t:123@my.logplex.example.com",
		HttpClient:	    client,
		RequestSizeTrigger: 100 * KB,
		Concurrency:	    3,
		Period:		    3 * time.Second,
	}

	c, err := logplexc.NewClient(&cfg)
	defer c.Close()
	// Messages will be periodically flushed.
	c.BufferMessage(...)

Those with advanced needs will want to use the low level
logplexc.MiniClient, which implements log formatting, buffering, and
HTTP POSTing.
*/
package logplexc

import (
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type Stats struct {
	// Number of concurrent requests at the time of retrieval.
	Concurrency int32

	// Message-level statistics

	// Total messages submitted
	Total uint64

	// Incremented when a message is ignored outright because of
	// too much work being done already.
	Dropped uint64

	// Incremented when a log post request is not known to have
	// succeeded and one has given up waiting.
	Cancelled uint64

	// Incremented when a log post request is responded to,
	// affirming that the messages have been rejected.
	Rejected uint64

	// Incremented only when a positive response is received from
	// logplex.
	Successful uint64

	// Request-level statistics

	TotalRequests   uint64
	DroppedRequests uint64
	CancelRequests  uint64
	RejectRequests  uint64
	SuccessRequests uint64
}

type TimeTriggerBehavior byte

const (
	// Carefully choose the zero-value so it is a reasonable
	// default, so that a user requesting the other behaviors --
	// which do not need a time -- can write things like:
	// TimeTrigger{Behavior: TimeTriggerImmediate} without
	// specifying a Period.
	TimeTriggerPeriodic TimeTriggerBehavior = iota
	TimeTriggerImmediate
	TimeTriggerNever
)

// A Logplex embeddable client implementation that includes
// concurrency, dropping, and statistics gathering.
type Client struct {
	s        Stats
	statLock sync.Mutex

	c *MiniClient

	// Concurrency control of POST workers: the current level of
	// concurrency, and a token bucket channel.
	bucket chan struct{}

	// Threshold of logplex request size to trigger POST.
	requestSizeTrigger int

	// For implementing timely flushing of log buffers.
	timeTrigger    TimeTriggerBehavior
	ticker         *time.Ticker
	tickerShutdown chan struct{}

	bucketDepth int
}

type Config struct {
	Logplex            url.URL
	HttpClient         http.Client
	RequestSizeTrigger int
	Concurrency        int
	Period             time.Duration

	// Optional: Can be set for advanced behaviors like triggering
	// Never or Immediately.
	TimeTrigger TimeTriggerBehavior
}

func NewClient(cfg *Config) (*Client, error) {
	c, err := NewMiniClient(
		&MiniConfig{
			Logplex:    cfg.Logplex,
			HttpClient: cfg.HttpClient,
		})

	if err != nil {
		return nil, err
	}

	m := Client{
		c:                  c,
		bucket:             make(chan struct{}),
		requestSizeTrigger: cfg.RequestSizeTrigger,
	}

	// Handle determining m.timeTrigger.  This complexity seems
	// reasonable to allow the user to get some input checking
	// (negative Periods) and to get TimeTriggerImmediate by
	// passing a zero-duration period (TimeTriggerImmediate is
	// still useful for internal bookkeeping).
	switch cfg.TimeTrigger {
	case TimeTriggerPeriodic:
		if cfg.Period < 0 {
			return nil, errors.New(
				"logplexc.Client: negative target " +
					"latency not allowed")
		} else if cfg.Period == 0 {
			// Rewrite a zero-duration period into an
			// immediate flush.
			m.timeTrigger = TimeTriggerImmediate
		} else if cfg.Period > 0 {
			m.timeTrigger = TimeTriggerPeriodic
		} else {
			panic("bug")
		}
	default:
		m.timeTrigger = cfg.TimeTrigger
	}

	// Supply tokens to do work with bounded concurrency.
	m.bucketDepth = cfg.Concurrency
	go func() {
		for i := 0; i < m.bucketDepth; i += 1 {
			m.bucket <- struct{}{}
		}
	}()

	// Set up the time-based log flushing, if requested.
	if m.timeTrigger == TimeTriggerPeriodic {
		m.ticker = time.NewTicker(cfg.Period)
		m.tickerShutdown = make(chan struct{})

		go func() {
			for {
				// Wait for a while to do work, or to
				// exit when the ticker is stopped
				// when Close is called on the client.
				select {
				case <-m.ticker.C:
					m.maybeWork()
				case <-m.tickerShutdown:
					return
				}
			}
		}()
	}

	return &m, nil
}

func (m *Client) Close() {
	// Clean up otherwise immortal ticker goroutine.
	m.ticker.Stop()
	m.tickerShutdown <- struct{}{}

	// Make an attempt to send the final buffer, if any.
	m.maybeWork()

	// Drain all work tokens.
	for i := 0; i < m.bucketDepth; i += 1 {
		<-m.bucket
	}

	close(m.bucket)
}

func (m *Client) BufferMessage(
	priority int, when time.Time, host string, procId string,
	log []byte) error {

	s := m.c.BufferMessage(priority, when, host, procId, log)
	if s.Buffered >= m.requestSizeTrigger ||
		m.timeTrigger == TimeTriggerImmediate {
		m.maybeWork()
	}

	return nil
}

func (m *Client) Statistics() (s Stats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()

	s = m.s
	return s
}

func (m *Client) maybeWork() {
	atomic.AddInt32(&m.s.Concurrency, 1)
	defer atomic.AddInt32(&m.s.Concurrency, -1)

	b := m.c.SwapBundle()

	// Avoid sending empty requests
	if b.NumberFramed <= 0 {
		return
	}

	// Check if there are any worker tokens available. If not,
	// then just abort after recording drop statistics.
	select {
	case <-m.bucket:
		go m.postBundle(&b)
	default:
		m.statReqDrop(&b.MiniStats)

		// In GOMAXPROCS=1 cases, tight loops can starve out
		// any of the workers predictably and seemingly
		// forever.
		runtime.Gosched()
	}
}

func (m *Client) postBundle(b *Bundle) {
	// When exiting, free up the token for use by another
	// worker.
	defer func() { m.bucket <- struct{}{} }()

	// Post to logplex.
	resp, err := m.c.Post(b)
	if err != nil {
		m.statReqErr(&b.MiniStats)
		return
	}

	defer resp.Body.Close()

	// Check HTTP return code and accrue statistics accordingly.
	if resp.StatusCode != http.StatusNoContent {
		m.statReqRej(&b.MiniStats)
	} else {
		m.statReqSuccess(&b.MiniStats)
	}
}

func (m *Client) statReqTotalUnsync(s *MiniStats) {
	m.s.Total += s.NumberFramed
	m.s.TotalRequests += 1
}

func (m *Client) statReqSuccess(s *MiniStats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()
	m.statReqTotalUnsync(s)

	m.s.Successful += s.NumberFramed
	m.s.SuccessRequests += 1
}

func (m *Client) statReqErr(s *MiniStats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()
	m.statReqTotalUnsync(s)

	m.s.Cancelled += s.NumberFramed
	m.s.CancelRequests += 1
}

func (m *Client) statReqRej(s *MiniStats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()
	m.statReqTotalUnsync(s)

	m.s.Rejected += s.NumberFramed
	m.s.RejectRequests += 1
}

func (m *Client) statReqDrop(s *MiniStats) {
	m.statLock.Lock()
	defer m.statLock.Unlock()
	m.statReqTotalUnsync(s)

	m.s.Dropped += s.NumberFramed
	m.s.DroppedRequests += 1
}
