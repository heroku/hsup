package hsup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestControlApiGetStatus(t *testing.T) {
	c := newControlAPI()
	c.processes = &Processes{
		Executors: []*Executor{
			{
				ProcessType: "web",
				IPAddress:   "0.0.0.0",
				Port:        5000,
				State:       Started,
			},
			{
				ProcessType: "worker",
				IPAddress:   "1.1.1.1",
				Port:        6000,
				State:       Retiring,
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

	c := newControlAPI()
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

func assert(t *testing.T, a, b interface{}) {
	if a != b {
		t.Fatalf("expected %v; was %v", a, b)
	}
}
