package hsup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.google.com/p/go-uuid/uuid"
)

func TestControlApiGetStatus(t *testing.T) {
	c, _ := NewControlAPI("", nil)
	c.processes = &Processes{
		Executors: []*Executor{
			{
				ProcessType: "web",
				State:       Started,
				IPInfo:      stubIPInfo("0.0.0.0", 5000),
			},
			{
				ProcessType: "worker",
				State:       Retiring,
				IPInfo:      stubIPInfo("1.1.1.1", 6000),
			},
		},
	}

	w := httptest.NewRecorder()
	r, err := http.NewRequest("GET", "http://example.com/status", nil)
	assert(t, nil, err)

	c.ServeHTTP(w, r)
	assert(t, http.StatusOK, w.Code)
	assert(t, "application/json", w.Header().Get("Content-Type"))

	var response StatusResponse
	err = json.NewDecoder(w.Body).Decode(&response)

	assert(t, nil, err)
	assert(t, 2, len(response.Processes))

	assert(t, "0.0.0.0", response.Processes["web"].IPAddress)
	assert(t, 5000, response.Processes["web"].Port)
	assert(t, "Started", response.Processes["web"].Status)

	assert(t, "1.1.1.1", response.Processes["worker"].IPAddress)
	assert(t, 6000, response.Processes["worker"].Port)
	assert(t, "Retiring", response.Processes["worker"].Status)
}

func TestControlApiPostControlStop(t *testing.T) {
	body := bytes.NewBuffer([]byte{})
	json.NewEncoder(body).Encode(StopRequest{
		Processes: []string{"web", "worker"},
	})

	c, _ := NewControlAPI("", nil)
	c.processes = &Processes{
		Executors: []*Executor{
			{
				ProcessType: "web",
				NewInput:    make(chan DynoInput),
			},
			{
				ProcessType: "worker",
				NewInput:    make(chan DynoInput),
			},
		},
	}

	for _, ex := range c.processes.Executors {
		go func(input chan DynoInput) {
			<-input // drain channels
		}(ex.NewInput)
	}

	w := httptest.NewRecorder()
	r, err := http.NewRequest("POST", "http://example.com/control/stop", body)
	assert(t, nil, err)

	c.ServeHTTP(w, r)
	assert(t, http.StatusOK, w.Code)
	assert(t, "application/json", w.Header().Get("Content-Type"))

	var response StopResponse
	err = json.NewDecoder(w.Body).Decode(&response)
	assert(t, nil, err)
	assert(t, 2, len(response.StoppedProcesses))
	assert(t, "web", response.StoppedProcesses[0])
	assert(t, "worker", response.StoppedProcesses[1])
}

func TestListenCreatesAndRemovesSocket(t *testing.T) {
	socket := filepath.Join("/", "tmp", uuid.New()+".sock")
	procs := make(chan *Processes)
	api, _ := NewControlAPI(socket, procs)

	go func(t *testing.T) {
		if err := api.Listen(); !isAllowedError(err) {
			t.Fatal(err)
		}
	}(t)

	_, err := retryUntil(5, time.Second, func() (bool, error) {
		rerr := api.ping()
		return rerr == nil, rerr
	})
	assert(t, nil, err)

	stat, err := os.Stat(socket)
	assert(t, nil, err)
	assert(t, os.ModeSocket|SocketPerm, stat.Mode())

	_, err = retryUntil(5, time.Second, func() (bool, error) {
		_, rerr := os.Stat(socket)
		return rerr == nil, rerr
	})
	assert(t, nil, err)

	api.Close()

	_, err = retryUntil(5, time.Second, func() (bool, error) {
		_, rerr := os.Stat(socket)
		return os.IsNotExist(rerr), rerr
	})
	assert(t, true, os.IsNotExist(err))
}

func TestListenErrorsSocketInUse(t *testing.T) {
	socket := filepath.Join("/", "tmp", uuid.New()+".sock")
	api, _ := NewControlAPI(socket, make(chan *Processes))
	go func(t *testing.T) {
		if err := api.Listen(); !isAllowedError(err) {
			t.Fatal(err)
		}
	}(t)

	defer api.Close()

	_, err := retryUntil(5, time.Second, func() (bool, error) {
		rerr := api.ping()
		return rerr == nil, rerr
	})
	assert(t, nil, err)

	anotherApi, _ := NewControlAPI(socket, make(chan *Processes))
	assert(t, ErrSocketInUse, anotherApi.Listen())
}

func TestNewListenerReusesSocket(t *testing.T) {
	socket := filepath.Join("/", "tmp", uuid.New()+".sock")
	_, err := os.Create(socket)
	assert(t, nil, err)

	_, err = os.Stat(socket)
	assert(t, nil, err)

	api, _ := NewControlAPI(socket, make(chan *Processes))

	go func(t *testing.T) {
		if err := api.Listen(); !isAllowedError(err) {
			t.Fatal(err)
		}
	}(t)

	defer api.Close()

	_, err = retryUntil(5, time.Second, func() (bool, error) {
		rerr := api.ping()
		return rerr == nil, rerr
	})
	assert(t, nil, err)
}

func assert(t *testing.T, a, b interface{}) {
	if a != b {
		t.Fatalf("expected %v; was %v", a, b)
	}
}

func stubIPInfo(ip string, port int) IPInfo {
	return func() (string, int) {
		return ip, port
	}
}

func isAllowedError(err error) bool {
	if err == nil {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}

func retryUntil(retries int, delay time.Duration, fn func() (bool, error)) (bool, error) {
	var success bool
	var err error

	for i := 0; i < retries; i++ {
		if i > 0 {
			time.Sleep(delay)
		}

		success, err = fn()
		if success {
			return true, err
		}
	}

	return success, err
}
